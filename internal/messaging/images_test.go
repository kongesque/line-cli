package messaging

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func imageFixture(t *testing.T) *PreparedImage {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	img, err := PrepareImage(context.Background(), Attachment{Name: "screenshot.png", Data: b.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

type imageAPI struct {
	*fakeAPI
	flow                   int
	flowErr                error
	full, preview, plain   []byte
	order                  []string
	fail, cancelAt         string
	cancel                 context.CancelFunc
	emptyOID, emptyReceipt bool
}

func (f *imageAPI) DetermineMediaMessageFlowContext(context.Context, string) (*line.MediaMessageFlowResponse, error) {
	return &line.MediaMessageFlowResponse{FlowMap: map[string]int{"1": f.flow}}, f.flowErr
}
func (f *imageAPI) step(stage string) error {
	f.order = append(f.order, stage)
	if stage == f.cancelAt {
		f.cancel()
	}
	if stage == f.fail {
		return errors.New("private server body")
	}
	return nil
}
func (f *imageAPI) UploadOBSWithSIDContext(_ context.Context, data []byte, sid string) (string, error) {
	if sid != "emi" {
		panic("wrong image SID")
	}
	f.full = append([]byte(nil), data...)
	err := f.step("attachment_upload")
	if f.emptyOID {
		return "", err
	}
	return "image-object", err
}
func (f *imageAPI) UploadOBSWithOIDAndSIDContext(_ context.Context, data []byte, oid, sid string) error {
	if oid != "image-object__ud-preview" || sid != "emi" {
		panic("wrong preview target")
	}
	f.preview = append([]byte(nil), data...)
	return f.step("preview_upload")
}
func (f *imageAPI) UploadOBSPlainContext(_ context.Context, data []byte, oid, kind string) error {
	if oid != "server-id" || kind != "image" {
		panic("wrong plaintext image target")
	}
	f.plain = append([]byte(nil), data...)
	return f.step("attachment_upload")
}
func (f *imageAPI) SendMessageContext(_ context.Context, seq int64, msg *line.Message) (*line.Message, error) {
	result, err := f.fakeAPI.SendMessage(seq, msg)
	if err != nil {
		return result, err
	}
	err = f.step("send")
	if f.emptyReceipt {
		return nil, err
	}
	return result, err
}
func TestImageSendFlows(t *testing.T) {
	img := imageFixture(t)
	for _, chat := range []string{"u-peer", "c-group", "r-room"} {
		for _, flow := range []int{1, 2} {
			t.Run(chat+string(rune('0'+flow)), func(t *testing.T) {
				c, f, crypto, store := setup(t, false)
				f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped"}
				api := &imageAPI{fakeAPI: f, flow: flow}
				crypt := &fileCrypto{fakeCrypto: crypto}
				c.crypto = crypt
				c.Session.NewClient = func(string) session.API { return api }
				result, err := c.SendImageContext(context.Background(), chat, *img, "")
				if err != nil {
					t.Fatal(err)
				}
				if result.ID != "server-id" || result.Encrypted != (flow == 2) || result.ChatID != chat || store.state.LastReqSeq != result.RequestSequence || f.sent.ContentType != 1 || !f.sent.HasContent {
					t.Fatal("incorrect image receipt/envelope")
				}
				if flow == 1 {
					if strings.Join(api.order, ",") != "send,attachment_upload" || !bytes.Equal(api.plain, img.file.Data) || len(f.sent.Chunks) != 0 || len(f.sent.ContentMetadata) != 0 {
						t.Fatal("incorrect plaintext image flow")
					}
				} else {
					if strings.Join(api.order, ",") != "attachment_upload,preview_upload,send" || crypt.contentType != 1 {
						t.Fatal("incorrect encrypted image flow")
					}
					var payload map[string]string
					if err := json.Unmarshal(crypt.payloadSent, &payload); err != nil {
						t.Fatal(err)
					}
					for _, pair := range [][2][]byte{{api.full, img.file.Data}, {api.preview, img.preview}} {
						data, err := decryptFile(pair[0], payload["keyMaterial"])
						if err != nil || !bytes.Equal(data, pair[1]) {
							t.Fatal("encrypted image or preview failed authentication")
						}
					}
					meta := f.sent.ContentMetadata
					if payload["fileName"] != "screenshot.jpg" || meta["OID"] != "image-object" || meta["SID"] != "emi" || meta["MEDIA_THUMB_INFO"] != `{"height":4,"width":8}` || meta["ENC_KM"] != "" || meta["FILE_NAME"] != "" {
						t.Fatal("incorrect image metadata")
					}
					encoded, _ := json.Marshal(result)
					if bytes.Contains(encoded, []byte(payload["keyMaterial"])) {
						t.Fatal("receipt leaked image key")
					}
				}
			})
		}
	}
}
func TestImageFailuresAndCancellationNeverReplay(t *testing.T) {
	img := imageFixture(t)
	for _, flow := range []int{1, 2} {
		stages := []string{"send", "attachment_upload"}
		if flow == 2 {
			stages = append(stages, "preview_upload")
		}
		for _, stage := range stages {
			for _, cancel := range []bool{false, true} {
				c, f, crypto, _ := setup(t, false)
				ctx, stop := context.WithCancel(context.Background())
				api := &imageAPI{fakeAPI: f, flow: flow, cancel: stop}
				if cancel {
					api.cancelAt = stage
				} else {
					api.fail = stage
				}
				c.crypto = &fileCrypto{fakeCrypto: crypto}
				c.Session.NewClient = func(string) session.API { return api }
				result, err := c.SendImageContext(ctx, "u-peer", *img, "")
				stop()
				// Cancellation after the final confirmed stage preserves success.
				if cancel && ((flow == 1 && stage == "attachment_upload") || (flow == 2 && stage == "send")) {
					if err != nil || result == nil {
						t.Fatal("lost confirmed image receipt", err)
					}
					continue
				}
				var mutation *MutationError
				if !errors.As(err, &mutation) || strings.Contains(err.Error(), "private") {
					t.Fatalf("unsafe/missing image outcome: %v", err)
				}
				if flow == 1 && stage == "attachment_upload" || flow == 1 && cancel && stage == "send" {
					if mutation.Mutation.Outcome != fault.Confirmed || mutation.Mutation.MessageID != "server-id" {
						t.Fatal("lost created image message")
					}
				} else if stage == "send" && !cancel {
					if mutation.Mutation.Outcome != fault.Unknown {
						t.Fatal("lost unknown send outcome")
					}
				} else if f.sends != 0 {
					t.Fatal("sent after failed or cancelled upload")
				}
				seen := map[string]bool{}
				for _, step := range api.order {
					if seen[step] {
						t.Fatal("mutation replayed")
					}
					seen[step] = true
				}
			}
		}
	}
}
func TestImageCapabilitiesAndValidationFailClosed(t *testing.T) {
	img := imageFixture(t)
	for _, mode := range []string{"missing-flow", "invalid-flow", "flow-error", "missing-key", "bad-key", "incomplete-members", "reply", "already-cancelled", "missing-oid", "missing-receipt"} {
		t.Run(mode, func(t *testing.T) {
			c, f, crypto, store := setup(t, false)
			api := &imageAPI{fakeAPI: f, flow: 2}
			c.crypto = &fileCrypto{fakeCrypto: crypto}
			c.Session.NewClient = func(string) session.API { return api }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			chat, reply := "u-peer", ""
			switch mode {
			case "missing-flow":
				api.flow = 0
			case "invalid-flow":
				api.flow = 3
			case "flow-error":
				api.flowErr = errors.New("transport failure")
			case "missing-key":
				f.key = nil
			case "bad-key":
				f.key.PublicKey = "bad"
			case "incomplete-members":
				chat = "c-group"
				f.groupErr = line.ErrGroupKeyNotFound
			case "reply":
				reply = "123"
			case "already-cancelled":
				cancel()
			case "missing-oid":
				api.emptyOID = true
			case "missing-receipt":
				api.emptyReceipt = true
			}
			_, err := c.SendImageContext(ctx, chat, *img, reply)
			if err == nil {
				t.Fatal("unsafe image send accepted")
			}
			if mode == "missing-receipt" {
				var m *MutationError
				if !errors.As(err, &m) || m.Mutation.Outcome != fault.Unknown || f.sends != 1 {
					t.Fatal("missing receipt outcome")
				}
				return
			}
			if f.sends != 0 || f.registrations != 0 || store.state.LastReqSeq != 0 {
				t.Fatal("mutation after invalid preparation")
			}
			if mode == "missing-oid" {
				var safe *fault.Error
				var mutation *MutationError
				if !errors.As(err, &safe) || safe.Code != "remote_failed" || safe.Stage != "attachment_upload" || !errors.As(err, &mutation) || mutation.Mutation.Steps[0].Outcome != fault.Unknown {
					t.Fatal("missing upload receipt classification", err)
				}
			}
			if mode != "missing-oid" && len(api.order) != 0 {
				t.Fatal("upload after invalid preparation")
			}
		})
	}
}
func TestImagePreparation(t *testing.T) {
	var b bytes.Buffer
	src := image.NewNRGBA(image.Rect(0, 0, 2000, 1000))
	if err := png.Encode(&b, src); err != nil {
		t.Fatal(err)
	}
	img, err := PrepareImage(context.Background(), Attachment{Name: "wrong.extension", Data: b.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img.file.Data))
	if err != nil || format != "jpeg" || cfg.Width*cfg.Height > 1280*1280 || cfg.Width != 1810 || cfg.Height != 905 || img.width != 384 || img.height != 192 || img.file.Name != "wrong.jpg" {
		t.Fatal("bad normalized image dimensions", cfg, err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(img.preview))
	if err != nil {
		t.Fatal(err)
	}
	r, g, bv, _ := decoded.At(0, 0).RGBA()
	if r < 65000 || g < 65000 || bv < 65000 {
		t.Fatal("transparent pixels must be white")
	}
	data := b.Bytes()
	animated := append([]byte(nil), data[:33]...)
	chunk := make([]byte, 20)
	binary.BigEndian.PutUint32(chunk, 8)
	copy(chunk[4:], "acTL")
	binary.BigEndian.PutUint32(chunk[8:], 2)
	binary.BigEndian.PutUint32(chunk[16:], crc32.ChecksumIEEE(chunk[4:16]))
	animated = append(animated, chunk...)
	animated = append(animated, data[33:]...)
	oversized := append([]byte(nil), data...)
	binary.BigEndian.PutUint32(oversized[16:], 20000)
	binary.BigEndian.PutUint32(oversized[29:], crc32.ChecksumIEEE(oversized[12:29]))
	pixels := append([]byte(nil), data...)
	binary.BigEndian.PutUint32(pixels[16:], 10000)
	binary.BigEndian.PutUint32(pixels[20:], 5000)
	binary.BigEndian.PutUint32(pixels[29:], crc32.ChecksumIEEE(pixels[12:29]))
	for _, bad := range [][]byte{nil, []byte("not an image"), []byte("GIF89a"), data[:len(data)/2], animated, oversized, pixels, make([]byte, MaxAttachmentBytes+1)} {
		if _, err := PrepareImage(context.Background(), Attachment{Name: "bad.png", Data: bad}); err == nil {
			t.Fatal("invalid/animated/oversized image accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareImage(ctx, Attachment{Name: "image.png", Data: data}); !errors.Is(err, context.Canceled) {
		t.Fatal("image preparation ignored cancellation")
	}
}
func TestImageEXIFOrientation(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			v := uint8(0)
			if x >= 10 {
				v = 255
			}
			src.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, src, nil); err != nil {
		t.Fatal(err)
	}
	for orientation := 1; orientation <= 8; orientation++ {
		exif := []byte{'E', 'x', 'i', 'f', 0, 0, 'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0, byte(orientation), 0, 0, 0, 0, 0, 0, 0}
		data := append([]byte{0xff, 0xd8, 0xff, 0xe1, 0, byte(len(exif) + 2)}, exif...)
		data = append(data, b.Bytes()[2:]...)
		if jpegOrientation(data) != orientation {
			t.Fatal("EXIF orientation lost")
		}
		img, err := PrepareImage(context.Background(), Attachment{Name: "camera.jpg", Data: data})
		if err != nil {
			t.Fatal(err)
		}
		dec, err := jpeg.Decode(bytes.NewReader(img.file.Data))
		if err != nil {
			t.Fatal(err)
		}
		if orientation >= 5 && dec.Bounds().Dx() != 10 || orientation < 5 && dec.Bounds().Dx() != 20 {
			t.Fatal("orientation dimensions incorrect")
		}
		r, _, _, _ := dec.At(0, 0).RGBA()
		white := orientation == 2 || orientation == 3 || orientation == 7 || orientation == 8
		if white != (r > 32767) {
			t.Fatal("orientation pixels incorrect", orientation)
		}
		if bytes.Contains(img.file.Data, []byte("Exif")) {
			t.Fatal("private EXIF metadata retained")
		}
	}
}
