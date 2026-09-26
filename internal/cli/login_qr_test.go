package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

const syntheticQRURL = "line://au/q/synthetic-session?secret=synthetic-public-key&e2eeVersion=1"

func qrApp(t *testing.T, input string) (*App, *guidedAPI, *bytes.Buffer, *bytes.Buffer, *int, *bool) {
	t.Helper()
	a, api, out, diagnostics, locked := guidedApp(t, input)
	a.Manager.Store = &initiallyEmptyStore{}
	a.Password = func() (string, error) { return "synthetic", nil }
	a.RenderQR = func(value string, _ QRTerminal) (QRFrame, error) {
		if value != syntheticQRURL {
			t.Fatal("renderer received incorrect value")
		}
		return QRFrame{Text: "[synthetic QR]\n", Columns: 14, Rows: 1}, nil
	}
	a.QRTerminal = func() QRTerminal { return QRTerminal{Width: 80} }
	calls := 0
	a.LoginQR = func(ctx context.Context, notify func(session.QRLoginEvent) error) (*line.Profile, error) {
		calls++
		if !*locked {
			t.Fatal("QR login without session lock")
		}
		for _, event := range []session.QRLoginEvent{
			{Kind: session.QRLoginCode, URL: syntheticQRURL, Attempt: 1, ExpiresIn: time.Minute},
			{Kind: session.QRLoginScanned}, {Kind: session.QRLoginPIN, PIN: "001234"},
			{Kind: session.QRLoginPhoneAccepted}, {Kind: session.QRLoginApproved},
		} {
			if err := notify(event); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := a.Manager.Store.Save(&session.State{AccessToken: "synthetic-access", Certificate: "synthetic-certificate", Generation: "new", MID: "synthetic-mid", CertificateOrigin: "qr", ExportedKeys: map[string]string{"1": "synthetic-export"}}); err != nil {
			return nil, err
		}
		return &line.Profile{DisplayName: "Synthetic"}, nil
	}
	return a, api, out, diagnostics, &calls, locked
}

func TestQRLoginSelectionAndStreams(t *testing.T) {
	for _, flags := range [][]string{nil, {"--qr"}, {"--qr-url"}, {"--qr", "--qr-url"}, {"--headless"}, {"--headless", "--qr-url"}, {"--email", "you@example.com"}, {"--headless", "--email", "you@example.com"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			a, api, out, diagnostics, calls, locked := qrApp(t, "")
			store := &fakeLoginStorage{}
			a.NewHeadlessLogin = func() (session.LoginStorage, error) { return store, nil }
			if err := a.Run(append([]string{"login"}, flags...)); err != nil {
				t.Fatal(err)
			}
			email := strings.Contains(strings.Join(flags, " "), "--email")
			if (*calls == 1) == email || (api.logins == 1) != email || *locked {
				t.Fatal("wrong login method or leaked lock")
			}
			if !strings.Contains(out.String(), "Session saved securely") || strings.Contains(out.String(), syntheticQRURL) || strings.Contains(out.String(), "001234") {
				t.Fatal("incorrect stdout")
			}
			if strings.Contains(strings.Join(flags, " "), "--qr-url") {
				if strings.Count(diagnostics.String(), syntheticQRURL) != 1 || !strings.Contains(diagnostics.String(), "scrollback") || !strings.Contains(diagnostics.String(), "online QR generator") {
					t.Fatal("missing explicit URL/warning")
				}
			} else if strings.Contains(diagnostics.String(), syntheticQRURL) {
				t.Fatal("raw QR URL exposed")
			}
			if !email && (!strings.Contains(diagnostics.String(), "001234") || !strings.Contains(diagnostics.String(), "Login approved.") || !strings.Contains(diagnostics.String(), "Securing this session")) {
				t.Fatal("missing QR states")
			}
			for _, secret := range []string{"synthetic-access", "synthetic-certificate"} {
				if strings.Contains(out.String()+diagnostics.String(), secret) {
					t.Fatal("saved secret exposed")
				}
			}
		})
	}
}

func TestLoginInvalidFlagsAndNoninteractiveQRNeverOpenStorage(t *testing.T) {
	for _, flags := range [][]string{{"--qr", "--email", "you@example.com"}, {"--qr-url", "--email", "you@example.com"}, {"--email", ""}, {"--email", "   "}, {"--password", "secret"}, {"extra"}, nil, {"--qr-url", "--force"}, {"--qr", "--force"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			a, _, _, _, calls, _ := qrApp(t, "")
			a.Interactive = false
			a.Lock = func() (func(), error) { t.Fatal("invalid login touched storage"); return nil, nil }
			if err := a.Run(append([]string{"login"}, flags...)); err == nil || !strings.Contains(err.Error(), "LINE was not contacted") || *calls != 0 {
				t.Fatal("missing preflight failure", err)
			}
		})
	}
}

func TestReplacementConfirmationForBothMethods(t *testing.T) {
	for _, method := range []string{"qr", "email"} {
		for _, answer := range []string{"y\n", "\n", "force"} {
			t.Run(method+answer, func(t *testing.T) {
				a, api, out, diagnostics, calls, locked := qrApp(t, answer)
				a.Manager.Store = &testStore{state: &session.State{AccessToken: "old", Generation: "old", MID: "old", Email: "saved@example.test"}}
				flags := []string{"login"}
				if method == "email" {
					flags = append(flags, "--email", "you@example.com")
				}
				if answer == "force" {
					flags = append(flags, "--force")
					a.In = errorInput{t: t}
				}
				if err := a.Run(flags); err != nil {
					t.Fatal(err)
				}
				want := 1
				if answer == "\n" {
					want = 0
					if out.String() != "Kept the existing session.\n" {
						t.Fatal(out.String())
					}
				}
				if *calls+api.logins != want || *locked {
					t.Fatal("incorrect replacement action")
				}
				if answer != "force" && (!strings.Contains(diagnostics.String(), "saved@example.test") || !strings.Contains(diagnostics.String(), "Continue? [y/N]")) {
					t.Fatal("missing confirmation")
				}
				if answer == "force" && (!strings.Contains(diagnostics.String(), "one Chrome-style session") || strings.Contains(diagnostics.String(), "Continue?")) {
					t.Fatal("force skipped warning or prompted")
				}
			})
		}
	}
}

type errorInput struct{ t *testing.T }

func (r errorInput) Read([]byte) (int, error) { r.t.Fatal("unexpected local input"); return 0, io.EOF }

func TestQRRejectsSessionChangesDuringConfirmation(t *testing.T) {
	for _, change := range []string{"generation", "invalidation", "deletion"} {
		t.Run(change, func(t *testing.T) {
			a, _, _, _, calls, locked := qrApp(t, "")
			s := &initiallyEmptyStore{testStore{state: &session.State{AccessToken: "old", Generation: "old", MID: "old"}}}
			a.Manager.Store = s
			a.In = checkedInput{t: t, locked: locked, Reader: strings.NewReader("y\n"), onRead: func() {
				switch change {
				case "generation":
					s.state.Generation = "new"
				case "invalidation":
					s.state.Invalidated = true
				case "deletion":
					s.state = nil
				}
			}}
			if err := a.Run([]string{"login"}); !errors.Is(err, session.ErrStorageChanged) || *calls != 0 || *locked {
				t.Fatal("changed session used", err)
			}
		})
	}
}

type callbackWriter struct {
	io.Writer
	before func()
}

func (w callbackWriter) Write(data []byte) (int, error) { w.before(); return w.Writer.Write(data) }
func TestQRForceStillChecksInitiallyAbsentSession(t *testing.T) {
	a, _, _, diagnostics, calls, _ := qrApp(t, "")
	s := a.Manager.Store.(*initiallyEmptyStore)
	a.Err = callbackWriter{diagnostics, func() { s.state = &session.State{AccessToken: "concurrent", Generation: "new"} }}
	if err := a.Run([]string{"login", "--force"}); !errors.Is(err, session.ErrStorageChanged) || *calls != 0 {
		t.Fatal("force bypassed snapshot", err)
	}
}

func TestHeadlessForceDoesNotBypassHostConsent(t *testing.T) {
	a, _, _, _, calls, _ := qrApp(t, "no\n")
	s := &fakeLoginStorage{consent: true}
	a.NewHeadlessLogin = func() (session.LoginStorage, error) { return s, nil }
	if err := a.Run([]string{"login", "--headless", "--force"}); !errors.Is(err, ErrCancelled) || *calls != 0 || s.accepted || !s.closed {
		t.Fatal("host consent bypassed", err)
	}
}

func TestQRNarrowTerminalErrorDoesNotExposeURL(t *testing.T) {
	a, _, out, diagnostics, _, _ := qrApp(t, "")
	a.RenderQR = nil
	a.QRTerminal = func() QRTerminal { return QRTerminal{Width: 20} }
	err := a.Run([]string{"login"})
	if err == nil || !strings.Contains(err.Error(), "at least") || !strings.Contains(err.Error(), "line login --qr-url") || strings.Contains(err.Error()+out.String()+diagnostics.String(), syntheticQRURL) || strings.Contains(out.String(), "Signed in") {
		t.Fatal("unsafe width error", err)
	}
}

func TestQRDebugModeRejectedBeforeStorage(t *testing.T) {
	t.Setenv("QRCODE_DEBUG", "1")
	a, _, _, _, _, _ := qrApp(t, "")
	a.Lock = func() (func(), error) { t.Fatal("debug preflight touched storage"); return nil, nil }
	if err := a.Run([]string{"login"}); err == nil || !strings.Contains(err.Error(), "QRCODE_DEBUG") {
		t.Fatal(err)
	}
}

func TestQRPresentationExpiryAndCountdown(t *testing.T) {
	for _, ansi := range []bool{false, true} {
		t.Run(fmt.Sprint(ansi), func(t *testing.T) {
			a, _, _, out, _, _ := qrApp(t, "")
			a.QRTerminal = func() QRTerminal { return QRTerminal{Width: 80, Height: 50, TTY: ansi, ANSI: ansi} }
			p := newQRPresenter(a, false, func() {})
			code := session.QRLoginEvent{Kind: session.QRLoginCode, URL: syntheticQRURL, Attempt: 1, ExpiresIn: time.Minute}
			if err := p.event(code); err != nil {
				t.Fatal(err)
			}
			first := out.String()
			p.tick(time.Now().Add(2 * time.Minute))
			if strings.Count(out.String(), "[synthetic QR]") != 1 {
				t.Fatal("countdown regenerated QR")
			}
			if ansi {
				if !strings.Contains(out.String(), "\r\x1b[2KWaiting for scan... expires in about 00:00") {
					t.Fatal("missing in-place approximate countdown")
				}
			} else if out.String() != first {
				t.Fatal("plain output received countdown spam")
			}
			if err := p.event(session.QRLoginEvent{Kind: session.QRLoginExpired, Attempt: 1}); err != nil {
				t.Fatal(err)
			}
			code.Attempt = 2
			if err := p.event(code); err != nil {
				t.Fatal(err)
			}
			if ansi {
				if !strings.Contains(out.String(), "\x1b[1A\r\x1b[2K") {
					t.Fatal("old QR not cleared")
				}
			} else if !strings.Contains(out.String(), "Previous QR expired; do not scan it.\nLINE login") || strings.Contains(out.String(), "\x1b") {
				t.Fatal("append-only expiry missing")
			}
			if err := p.event(session.QRLoginEvent{Kind: session.QRLoginScanned}); err != nil {
				t.Fatal(err)
			}
			before := out.String()
			p.tick(time.Now().Add(time.Second))
			if before != out.String() {
				t.Fatal("countdown continued after scan")
			}
		})
	}
}

func TestQRContextCancellationStopsDisplay(t *testing.T) {
	a, _, out, _, _, _ := qrApp(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Context = ctx
	a.LoginQR = func(ctx context.Context, notify func(session.QRLoginEvent) error) (*line.Profile, error) {
		if err := notify(session.QRLoginEvent{Kind: session.QRLoginCode, URL: syntheticQRURL}); err != nil {
			return nil, err
		}
		cancel()
		return nil, ctx.Err()
	}
	if err := a.Run([]string{"login"}); !errors.Is(err, context.Canceled) || strings.Contains(out.String(), "Signed in") {
		t.Fatal("cancellation reported success", err)
	}
}

func TestQRShortViewportAndResizeClearOldCode(t *testing.T) {
	for _, scenario := range []string{"short", "resize"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, _, out, _, _ := qrApp(t, "")
			info := QRTerminal{Width: 80, Height: 50, TTY: true, ANSI: true}
			if scenario == "short" {
				info.Height = 5
			}
			a.QRTerminal = func() QRTerminal { return info }
			p := newQRPresenter(a, false, func() {})
			if err := p.event(session.QRLoginEvent{Kind: session.QRLoginCode, URL: syntheticQRURL, Attempt: 1, ExpiresIn: time.Minute}); err != nil {
				t.Fatal(err)
			}
			if scenario == "resize" {
				info.Width = 70
			}
			if err := p.event(session.QRLoginEvent{Kind: session.QRLoginExpired, Attempt: 1}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "\x1b[2J\x1b[H") {
				t.Fatal("old code could remain visible after reflow")
			}
		})
	}
}

func TestSavedEmailDoesNotClaimRemoteValidityOrInjectTerminalControls(t *testing.T) {
	a, _, _, out, calls, _ := qrApp(t, "\n")
	a.Manager.Store = &testStore{state: &session.State{AccessToken: "old", Generation: "old", Email: "saved\x1b[2J\n@example.test"}}
	if err := a.Run([]string{"login"}); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 || strings.Contains(out.String(), "Already signed in") || strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "already saved") {
		t.Fatal("unsafe account confirmation")
	}
}

func TestNoninteractiveEmailNeedsForceForExistingSession(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			a, api, _, _, _, _ := qrApp(t, "")
			a.Interactive = false
			a.Manager.Store = &testStore{state: &session.State{AccessToken: "old", Generation: "old"}}
			flags := []string{"login", "--email", "you@example.com"}
			if force {
				flags = append(flags, "--force")
			}
			err := a.Run(flags)
			if force {
				if err != nil || api.logins != 1 {
					t.Fatal(err)
				}
			} else if err == nil || api.logins != 0 || !strings.Contains(err.Error(), "--force") {
				t.Fatal("replacement without confirmation", err)
			}
		})
	}
}
