package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/kongesque/line-cli/internal/session"
)

// All writes (events and countdown ticks) are serialized. The presenter owns no
// expiry policy: reaching zero leaves the existing code and scan request alone.
type qrPresenter struct {
	app        *App
	urlOnly    bool
	cancel     context.CancelFunc
	mu         sync.Mutex
	terminal   QRTerminal
	deadline   time.Time
	waiting    bool
	qrLines    int
	err        error
	stop, done chan struct{}
}

func newQRPresenter(a *App, urlOnly bool, cancel context.CancelFunc) *qrPresenter {
	return &qrPresenter{app: a, urlOnly: urlOnly, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{})}
}

func (p *qrPresenter) start(ctx context.Context) {
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				p.tick(now)
			case <-p.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (p *qrPresenter) close() {
	close(p.stop)
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endWaiting()
}

func (p *qrPresenter) write(text string) {
	if p.err != nil {
		return
	}
	_, p.err = io.WriteString(p.app.Err, text)
	if p.err != nil {
		p.cancel()
	}
}

// Text here is static ASCII prose. Secrets are handled explicitly at their
// display sites, and QR rows bypass wrapping so modules are never clipped.
func (p *qrPresenter) prose(text string) int {
	width := p.terminal.Width
	if width <= 0 {
		width = 80
	}
	var out strings.Builder
	for _, line := range strings.Split(text, "\n") {
		for len(line) > width {
			cut := strings.LastIndexByte(line[:width+1], ' ')
			if cut <= 0 {
				cut = width
			}
			out.WriteString(line[:cut])
			out.WriteByte('\n')
			line = strings.TrimLeft(line[cut:], " ")
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	value := out.String()
	p.write(value)
	return strings.Count(value, "\n")
}

func (p *qrPresenter) countdown(now time.Time) string {
	seconds := max(0, int(p.deadline.Sub(now).Seconds()+0.999))
	return fmt.Sprintf("Waiting for scan... expires in about %02d:%02d. Ctrl-C to cancel", seconds/60, seconds%60)
}

func (p *qrPresenter) tick(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.waiting || p.err != nil {
		return
	}
	current := p.app.qrTerminal()
	if !current.ANSI || current.Width != p.terminal.Width {
		p.endWaiting()
		return
	}
	p.write("\r\x1b[2K" + p.countdown(now))
}

func (p *qrPresenter) endWaiting() {
	if p.waiting {
		p.write("\n")
		p.waiting = false
		p.qrLines++
	}
}

func (p *qrPresenter) eraseQR() {
	p.endWaiting()
	if p.qrLines == 0 || !p.terminal.ANSI || p.urlOnly {
		return
	}
	current := p.app.qrTerminal()
	if !current.ANSI {
		return
	}
	if current.Width != p.terminal.Width || current.Height <= p.qrLines {
		// The code may have scrolled or reflowed; clearing the visible screen
		// avoids moving beyond the current viewport into unrelated content.
		p.write("\x1b[2J\x1b[H")
	} else {
		for i := 0; i < p.qrLines; i++ {
			p.write("\x1b[1A\r\x1b[2K")
		}
	}
	p.qrLines = 0
}

func (p *qrPresenter) event(event session.QRLoginEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	switch event.Kind {
	case session.QRLoginCode:
		p.terminal = p.app.qrTerminal()
		if event.Attempt > 1 && (!p.terminal.ANSI || p.urlOnly) {
			p.prose("Previous QR expired; do not scan it.")
		}
		var frame QRFrame
		if !p.urlOnly {
			render := p.app.RenderQR
			if render == nil {
				render = renderTerminalQR
			}
			var err error
			frame, err = render(event.URL, p.terminal)
			if err != nil {
				return err
			}
		}
		p.qrLines = p.prose("LINE login\n")
		if p.urlOnly {
			p.prose("Encode this one-time value as a QR code using a trusted local tool:")
			p.write(terminalText(event.URL) + "\n\n")
			p.prose("It expires shortly and remains in terminal scrollback. Do not send it to an online QR generator, share it, or save it.\n")
			p.prose("Waiting for scan... Ctrl-C to cancel")
		} else {
			p.qrLines += p.prose("Scan this code with the QR scanner in LINE on your phone.\n")
			p.write(frame.Text)
			p.qrLines += frame.Rows
			p.qrLines += p.prose("")
			p.deadline = time.Now().Add(event.ExpiresIn)
			status := p.countdown(time.Now())
			if p.terminal.ANSI && event.ExpiresIn > 0 && len(status) < p.terminal.Width {
				p.write(status)
				p.waiting = true
			} else {
				p.qrLines += p.prose("Waiting for scan... Ctrl-C to cancel")
			}
		}
	case session.QRLoginExpired:
		p.eraseQR()
		p.prose("That QR code expired before it was scanned.")
		if event.Attempt < session.QRLoginMaxAttempts {
			p.prose(fmt.Sprintf("Creating a new code... attempt %d of %d", event.Attempt+1, session.QRLoginMaxAttempts))
		}
	case session.QRLoginScanned:
		p.endWaiting()
		p.prose("QR scanned.")
	case session.QRLoginPIN:
		p.prose("")
		p.write("Verification code: " + terminalText(event.PIN) + "\n")
		p.prose("Enter this code in LINE on your phone.\n\nWaiting for phone approval... Ctrl-C to cancel")
	case session.QRLoginPhoneAccepted:
		p.prose("Phone verification accepted.")
	case session.QRLoginApproved:
		p.prose("Login approved.\nSecuring this session...")
	}
	return p.err
}
