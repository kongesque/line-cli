package messaging

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"math"
	"path/filepath"
	"strings"

	"github.com/kongesque/line-cli/internal/fault"
)

// MaxImagePixels bounds decoding memory independently of the compressed file size.
const MaxImagePixels = 40_000_000
const MaxImageDimension = 16384

// PreparedImage contains validated, normalized still-image bytes. Private fields
// prevent callers from bypassing PrepareImage's decoding and resource limits.
type PreparedImage struct {
	file          Attachment
	preview       []byte
	width, height int
}

// PrepareImage accepts JPEG and nonanimated PNG, regardless of filename suffix.
// Match Chrome's ordinary image mode: white-backed JPEG, at most 1280² pixels,
// quality 60, and a 384×384 bounding-box JPEG preview at quality 30. Encoders and
// resampling differ from browser canvas; byte-for-byte parity is not intended.
func PrepareImage(ctx context.Context, file Attachment) (*PreparedImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := file.Validate(); err != nil {
		return nil, err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(file.Data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errors.New("image must contain valid JPEG or still PNG data")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxImageDimension || cfg.Height > MaxImageDimension || int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return nil, errors.New("image exceeds the CLI limit of 40 million pixels or 16384 pixels per dimension")
	}
	if format == "png" {
		// Parse chunk boundaries; a byte substring search can match compressed pixels.
		for offset := 8; offset+12 <= len(file.Data); {
			size := uint64(binary.BigEndian.Uint32(file.Data[offset:]))
			if size+12 > uint64(len(file.Data)-offset) {
				return nil, errors.New("invalid PNG data")
			}
			if string(file.Data[offset+4:offset+8]) == "acTL" {
				return nil, errors.New("animated PNG images are not supported")
			}
			offset += int(size) + 12
		}
	}
	src, _, err := image.Decode(bytes.NewReader(file.Data))
	if err != nil {
		return nil, errors.New("could not decode complete image data")
	}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegOrientation(file.Data)
	}
	w, h := cfg.Width, cfg.Height
	if orientation >= 5 {
		w, h = h, w
	}
	scale := math.Min(1, math.Sqrt(float64(1280*1280)/float64(w*h)))
	full, err := imageJPEG(ctx, src, orientation, max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale)), 60)
	if err != nil {
		return nil, err
	}
	scale = math.Min(1, math.Min(384/float64(w), 384/float64(h)))
	pw, ph := max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))
	preview, err := imageJPEG(ctx, src, orientation, pw, ph, 30)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(file.Name, filepath.Ext(file.Name)) + ".jpg"
	prepared := &PreparedImage{file: Attachment{Name: name, Data: full}, preview: preview, width: pw, height: ph}
	if err := prepared.file.Validate(); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (c *Client) SendImageContext(ctx context.Context, chat string, img PreparedImage, replyTo string) (*SendResult, error) {
	if replyTo != "" {
		return nil, fault.New("invalid_arguments", "validation", "--image cannot be combined with --reply-to; image reply support is unverified", nil)
	}
	if len(img.file.Data) == 0 || len(img.preview) == 0 {
		return nil, fault.New("invalid_arguments", "validation", "image must be prepared before sending", nil)
	}
	return c.send(ctx, chat, "", replyTo, &img.file, nil, &img)
}

func (c *Client) uploadAttachment(ctx context.Context, file *Attachment, img *PreparedImage, metadata map[string]string, op *fault.Mutation) ([]byte, error) {
	if img == nil {
		return c.uploadFile(ctx, file, metadata, op)
	}
	return nil, encryptedImageUnavailable()
}

func encryptedImageUnavailable() error {
	return fault.New("encrypted_image_unavailable", "preparation", "encrypted image sending is disabled because the full image and preview reuse an encryption stream; nothing was sent", nil)
}

// Read EXIF orientation without copying metadata (including GPS) into output.
func jpegOrientation(data []byte) int {
	for p := 2; p+4 <= len(data); {
		if data[p] != 0xff {
			break
		}
		marker := data[p+1]
		p += 2
		if marker == 0xda || marker == 0xd9 {
			break
		}
		n := int(binary.BigEndian.Uint16(data[p:]))
		if n < 2 || n > len(data)-p {
			break
		}
		segment := data[p+2 : p+n]
		p += n
		if marker != 0xe1 || len(segment) < 14 || string(segment[:6]) != "Exif\x00\x00" {
			continue
		}
		tiff := segment[6:]
		var order binary.ByteOrder
		switch string(tiff[:2]) {
		case "II":
			order = binary.LittleEndian
		case "MM":
			order = binary.BigEndian
		default:
			continue
		}
		if order.Uint16(tiff[2:4]) != 42 {
			continue
		}
		offset := uint64(order.Uint32(tiff[4:8]))
		if offset+2 > uint64(len(tiff)) {
			continue
		}
		count := int(order.Uint16(tiff[offset : offset+2]))
		for i := 0; i < count; i++ {
			pos := offset + 2 + uint64(i)*12
			if pos+12 > uint64(len(tiff)) {
				break
			}
			entry := tiff[pos : pos+12]
			if order.Uint16(entry[:2]) == 0x112 && order.Uint16(entry[2:4]) == 3 && order.Uint32(entry[4:8]) == 1 {
				o := int(order.Uint16(entry[8:10]))
				if o >= 1 && o <= 8 {
					return o
				}
			}
		}
	}
	return 1
}

func imageJPEG(ctx context.Context, src image.Image, orientation, w, h, quality int) ([]byte, error) {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	ow, oh := sw, sh
	if orientation >= 5 {
		ow, oh = sh, sw
	}
	pixel := func(x, y int) color.RGBA {
		switch orientation {
		case 2:
			x = sw - 1 - x
		case 3:
			x, y = sw-1-x, sh-1-y
		case 4:
			y = sh - 1 - y
		case 5:
			x, y = y, x
		case 6:
			x, y = y, sh-1-x
		case 7:
			x, y = sw-1-y, sh-1-x
		case 8:
			x, y = sw-1-y, x
		}
		r, g, b, a := src.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
		return color.RGBA{uint8((r + 65535 - a) >> 8), uint8((g + 65535 - a) >> 8), uint8((b + 65535 - a) >> 8), 255}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fy := math.Max(0, (float64(y)+.5)*float64(oh)/float64(h)-.5)
		y0 := int(fy)
		dy := fy - float64(y0)
		for x := 0; x < w; x++ {
			fx := math.Max(0, (float64(x)+.5)*float64(ow)/float64(w)-.5)
			x0 := int(fx)
			dx := fx - float64(x0)
			a, b, c, d := pixel(x0, y0), pixel(min(x0+1, ow-1), y0), pixel(x0, min(y0+1, oh-1)), pixel(min(x0+1, ow-1), min(y0+1, oh-1))
			blend := func(a, b, c, d uint8) uint8 {
				return uint8(math.Round((float64(a)*(1-dx)+float64(b)*dx)*(1-dy) + (float64(c)*(1-dx)+float64(d)*dx)*dy))
			}
			dst.SetRGBA(x, y, color.RGBA{blend(a.R, b.R, c.R, d.R), blend(a.G, b.G, c.G, d.G), blend(a.B, b.B, c.B, d.B), 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, errors.New("could not encode image")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
