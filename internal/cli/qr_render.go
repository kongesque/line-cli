package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	qrcode "github.com/yeqown/go-qrcode/v2"
	"golang.org/x/term"
)

// QRTerminal describes stderr, independently of the interactive input stream.
type QRTerminal struct {
	Width, Height           int
	TTY, ANSI, Colors, Dark bool
}

type QRFrame struct {
	Text          string
	Columns, Rows int
}

type QRRenderer func(value string, terminal QRTerminal) (QRFrame, error)

type qrWidthError struct{ columns int }

func (e *qrWidthError) Error() string {
	return fmt.Sprintf("This terminal is too narrow to show a scannable QR code. Widen it to at least %d columns, or run: line login --qr-url.", e.columns)
}

func qrDebugCheck() error {
	// The encoder's optional debug mode can write QR images and encoded data.
	// Never enter that mode for a login value, even if it was enabled in the
	// inherited environment. --qr-url does not invoke the encoder.
	switch os.Getenv("QRCODE_DEBUG") {
	case "1", "true", "TRUE", "enabled", "ENABLED":
		return errors.New("unset QRCODE_DEBUG before QR login, or use line login --qr-url")
	}
	return nil
}

func (a *App) qrTerminal() QRTerminal {
	var info QRTerminal
	if a.QRTerminal != nil {
		info = a.QRTerminal()
	} else {
		info = QRTerminal{Width: 80, Dark: true}
		if file, ok := a.Err.(interface{ Fd() uintptr }); ok && term.IsTerminal(int(file.Fd())) {
			info.TTY = true
			width, height, err := term.GetSize(int(file.Fd()))
			if err == nil {
				info.Width, info.Height = width, height
			} else {
				info.Width = 0
			}
			name := strings.ToLower(os.Getenv("TERM"))
			for _, prefix := range []string{"xterm", "screen", "tmux", "rxvt", "vt", "linux", "ansi", "cygwin", "alacritty", "foot", "kitty", "wezterm"} {
				if strings.HasPrefix(name, prefix) {
					info.ANSI = true
					break
				}
			}
			info.Colors = info.ANSI
		}
		if parts := strings.Split(os.Getenv("COLORFGBG"), ";"); len(parts) >= 2 {
			if bg, err := strconv.Atoi(parts[len(parts)-1]); err == nil && bg >= 0 && bg <= 15 {
				info.Dark = bg < 7 || bg == 8
				info.Colors = false // Use the terminal's own monochrome contrast.
			}
		}
	}
	info.ANSI = info.TTY && info.ANSI
	_, noColor := os.LookupEnv("NO_COLOR")
	info.Colors = info.ANSI && info.Colors && !noColor
	return info
}

type qrMatrixWriter struct{ bitmap [][]bool }

func (w *qrMatrixWriter) Write(matrix qrcode.Matrix) error { w.bitmap = matrix.Bitmap(); return nil }
func (*qrMatrixWriter) Close() error                       { return nil }

func renderTerminalQR(value string, terminal QRTerminal) (QRFrame, error) {
	if err := qrDebugCheck(); err != nil {
		return QRFrame{}, err
	}
	code, err := qrcode.NewWith(value, qrcode.WithEncodingMode(qrcode.EncModeByte), qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionMedium))
	if err != nil {
		return QRFrame{}, errors.New("could not encode the login QR code; try line login --qr-url")
	}
	// ISO QR quiet zone: four modules on every edge. One module per column,
	// two per terminal row. The final half-row is padded with white.
	columns := code.Dimension() + 8
	if terminal.Width < columns {
		return QRFrame{}, &qrWidthError{columns: columns}
	}
	matrix := new(qrMatrixWriter)
	if err := code.Save(matrix); err != nil {
		return QRFrame{}, errors.New("could not render the login QR code")
	}
	var out strings.Builder
	color := terminal.TTY && terminal.ANSI && terminal.Colors
	ink := func(x, y int) bool {
		dark := x >= 4 && y >= 4 && x < columns-4 && y < columns-4 && matrix.bitmap[y-4][x-4]
		if terminal.Dark && !color {
			return !dark
		}
		return dark
	}
	for y := 0; y < columns; y += 2 {
		if color {
			out.WriteString("\x1b[30;47m")
		}
		for x := 0; x < columns; x++ {
			top, bottom := ink(x, y), ink(x, y+1)
			switch {
			case top && bottom:
				out.WriteRune('█')
			case top:
				out.WriteRune('▀')
			case bottom:
				out.WriteRune('▄')
			default:
				out.WriteByte(' ')
			}
		}
		if color {
			out.WriteString("\x1b[0m")
		}
		out.WriteByte('\n')
	}
	return QRFrame{Text: out.String(), Columns: columns, Rows: (columns + 1) / 2}, nil
}
