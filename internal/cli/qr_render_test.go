package cli

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	qrdecoder "github.com/makiuchi-d/gozxing/qrcode"
)

func TestRenderedHalfBlockQRDecodesIndependently(t *testing.T) {
	value := "line://au/q/synthetic-only?secret=AA%2B%2F0123456789012345678901234567890123456789%3D&e2eeVersion=1"
	for _, style := range []QRTerminal{{Width: 80}, {Width: 80, Dark: true}, {Width: 80, TTY: true, ANSI: true, Colors: true, Dark: true}} {
		t.Run(fmt.Sprintf("dark=%v color=%v", style.Dark, style.Colors), func(t *testing.T) {
			frame, err := renderTerminalQR(value, style)
			if err != nil {
				t.Fatal(err)
			}
			if frame.Columns > 80 || frame.Rows != (frame.Columns+1)/2 || strings.Contains(frame.Text, value) {
				t.Fatal("bad QR size or raw value disclosure")
			}
			text := strings.ReplaceAll(strings.ReplaceAll(frame.Text, "\x1b[30;47m", ""), "\x1b[0m", "")
			rows := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
			const scale = 5
			img := image.NewGray(image.Rect(0, 0, frame.Columns*scale, len(rows)*2*scale))
			for row, text := range rows {
				cells := []rune(text)
				if len(cells) != frame.Columns {
					t.Fatal("clipped QR row")
				}
				for x, cell := range cells {
					for half := 0; half < 2; half++ {
						foreground := cell == '█' || (half == 0 && cell == '▀') || (half == 1 && cell == '▄')
						dark := foreground
						if style.Dark && !style.Colors {
							dark = !foreground
						}
						shade := uint8(255)
						if dark {
							shade = 0
						}
						y := row*2 + half
						if (x < 4 || x >= frame.Columns-4 || y < 4 || y >= frame.Columns-4) && dark {
							t.Fatal("quiet zone is not four light modules")
						}
						for yy := y * scale; yy < (y+1)*scale; yy++ {
							for xx := x * scale; xx < (x+1)*scale; xx++ {
								img.SetGray(xx, yy, color.Gray{Y: shade})
							}
						}
					}
				}
			}
			bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
			if err != nil {
				t.Fatal(err)
			}
			result, err := qrdecoder.NewQRCodeReader().Decode(bitmap, nil)
			if err != nil || result.GetText() != value {
				t.Fatal("independent decoder could not recover the exact QR value", err)
			}
		})
	}
}

func TestQRWidthBoundaryAndNoClippedOutput(t *testing.T) {
	wide, err := renderTerminalQR("synthetic", QRTerminal{Width: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := renderTerminalQR("synthetic", QRTerminal{Width: wide.Columns}); err != nil {
		t.Fatal(err)
	}
	frame, err := renderTerminalQR("synthetic", QRTerminal{Width: wide.Columns - 1})
	var width *qrWidthError
	if !errors.As(err, &width) || width.columns != wide.Columns || frame.Text != "" {
		t.Fatal("clipped QR emitted")
	}
}

func TestQRTerminalHonorsNoColorAndRedirectedOutput(t *testing.T) {
	a, _, _, _, _, _ := qrApp(t, "")
	a.QRTerminal = func() QRTerminal { return QRTerminal{Width: 80, TTY: true, ANSI: true, Colors: true, Dark: true} }
	t.Setenv("NO_COLOR", "1")
	terminal := a.qrTerminal()
	if terminal.Colors || !terminal.ANSI {
		t.Fatal("NO_COLOR should disable colors while retaining TTY cursor capability")
	}
	frame, err := renderTerminalQR("synthetic", terminal)
	if err != nil || strings.Contains(frame.Text, "\x1b") {
		t.Fatal("colored QR despite NO_COLOR", err)
	}
	a.QRTerminal = func() QRTerminal { return QRTerminal{Width: 80, ANSI: true, Colors: true} }
	terminal = a.qrTerminal()
	if terminal.ANSI || terminal.Colors {
		t.Fatal("ANSI enabled for redirected stderr")
	}
	a.QRTerminal = nil
	t.Setenv("TERM", "dumb")
	terminal = a.qrTerminal()
	if terminal.ANSI || terminal.TTY || terminal.Width != 80 {
		t.Fatal("incorrect append-only fallback")
	}
}
