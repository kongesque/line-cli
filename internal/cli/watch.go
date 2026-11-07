package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/kongesque/line-cli/internal/events"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func (a *App) watchCommand(args []string) error {
	fs := newFlagSet("watch")
	h := commandHelp{Command: "watch", Usage: []string{"line watch [options]"},
		About:    "Stream new messages and events. In a terminal, prints one readable line per\nmessage; when piped, or with --json, writes one JSON object per line. Starts\nnow on first use and resumes from the saved position on later runs. Never\nsends read receipts.",
		Groups:   []flagGroup{{"Stream", []string{"from-now", "limit", "timeout"}}, {"Output", []string{"json", "verbose"}}},
		Examples: []string{"line watch", "line watch --json | your-program", "line watch --limit 10 --timeout 5m"}}
	jsonOutput := fs.Bool("json", true, "write newline-delimited JSON (default when stdout is not a terminal)")
	verbose := fs.Bool("verbose", false, "in readable output, also show non-message events")
	fromNow := fs.Bool("from-now", false, "discard the saved resume position and start at the current revision")
	limit := fs.Int("limit", 0, "stop after N emitted events; 0 watches continuously")
	timeout := fs.Duration("timeout", 0, "stop after this duration, e.g. 30s; 0 watches continuously")
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	if fs.NArg() != 0 || *limit < 0 || *timeout < 0 || !*jsonOutput {
		return errors.New("invalid watch arguments; run line watch --help")
	}
	jsonSet := false
	fs.Visit(func(f *flag.Flag) { jsonSet = jsonSet || f.Name == "json" })
	// Piped output and --json keep the NDJSON stream unchanged.
	readable := a.StdoutIsTerminal && !jsonSet
	ctx := a.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	lock := a.WatchLock
	if lock == nil {
		lock = session.WatchLock
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	fmt.Fprintln(a.Err, "Watching LINE events. Press Ctrl-C to stop.")
	w := &events.Watcher{Manager: a.Manager, Lock: a.Lock, Out: a.Out, Err: a.Err, FromNow: *fromNow, Limit: *limit}
	if readable {
		w.Emit = (&readableWatch{a: a, verbose: *verbose, names: make(map[string]string)}).emit
	}
	err = w.Run(ctx)
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}

// readableWatch prints one line per message for a terminal. Names are looked
// up once per ID and cached; lookup failures fall back to IDs.
type readableWatch struct {
	a       *App
	verbose bool
	self    string
	names   map[string]string
}

func (r *readableWatch) emit(ctx context.Context, e *events.Event) (bool, error) {
	var line string
	switch {
	case e.Event == "message" && e.Message != nil:
		line = r.message(ctx, e)
	case e.Event == "resync_required":
		line = "—  LINE skipped ahead; some events may be missing. Check recent messages in LINE."
	case r.verbose:
		line = fmt.Sprintf("—  Event type %d (revision %s)", e.Type, terminalText(e.Revision))
	default:
		return false, nil
	}
	if _, err := fmt.Fprintln(r.a.Out, line); err != nil {
		return false, errors.New("could not write watch output; the event will replay on restart")
	}
	return true, nil
}

func (r *readableWatch) message(ctx context.Context, e *events.Event) string {
	m := e.Message
	if r.self == "" {
		if s, err := r.a.Manager.Store.Load(); err == nil && s != nil {
			r.self = s.MID
		}
	}
	stamp := "--:--"
	if millis, err := m.CreatedTime.Int64(); err == nil && millis > 0 {
		t := time.UnixMilli(millis).Local()
		stamp = t.Format("15:04")
		if y, d := t.YearDay(), time.Now().YearDay(); y != d || t.Year() != time.Now().Year() {
			stamp = t.Format("2006-01-02 15:04")
		}
	}
	r.lookup(ctx, e.ChatID, m.From)
	sender := r.name(m.From)
	body := messagePreview(*m)
	if m.ContentType == 0 && m.Error == "" && m.Content.Kind != "location" && m.Content.Status != "malformed_metadata" {
		body = m.Text
	}
	// One line per message: mark line breaks, then neutralize control text.
	body = terminalText(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", " ↵ "))
	if chatKind(e.ChatID) == "Direct" {
		if m.From == r.self && r.self != "" {
			return fmt.Sprintf("%s  You → %s: %s", stamp, terminalText(r.name(e.ChatID)), body)
		}
		return fmt.Sprintf("%s  %s: %s", stamp, terminalText(sender), body)
	}
	return fmt.Sprintf("%s  %s · %s: %s", stamp, terminalText(r.name(e.ChatID)), terminalText(sender), body)
}

func (r *readableWatch) name(id string) string {
	if id != "" && id == r.self {
		return "You"
	}
	if name := r.names[id]; name != "" {
		return name
	}
	if id == "" {
		return "Unknown sender"
	}
	return id
}

// lookup resolves unseen chat and sender names once; failures are cached too.
func (r *readableWatch) lookup(ctx context.Context, chat, sender string) {
	var users []string
	for _, id := range []string{chat, sender} {
		if _, seen := r.names[id]; seen || id == "" || id == r.self {
			continue
		}
		r.names[id] = ""
		if chatKind(id) == "Direct" {
			users = append(users, id)
		} else if chatKind(id) == "Group" {
			var response *line.GetChatsResponse
			if err := r.a.Manager.DoContext(ctx, func(api session.API) (err error) {
				response, err = api.GetChatsContext(ctx, []string{id}, false, false)
				return
			}); err == nil && response != nil {
				for _, c := range response.Chats {
					if c.ChatMid == id && strings.TrimSpace(c.ChatName) != "" {
						r.names[id] = c.ChatName
					}
				}
			}
		}
	}
	if len(users) == 0 {
		return
	}
	var response *line.ContactsResponse
	if err := r.a.Manager.DoContext(ctx, func(api session.API) (err error) {
		response, err = api.GetContactsV2Context(ctx, users)
		return
	}); err == nil && response != nil {
		for id, c := range response.Contacts {
			if _, asked := r.names[id]; asked {
				r.names[id] = c.Contact.EffectiveDisplayName()
			}
		}
	}
}
