package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/internal/update"
	"github.com/kongesque/line-cli/pkg/e2ee"
	"github.com/kongesque/line-cli/pkg/line"
)

// Doctor output is meant to be pasted into a public issue. Checks report only
// allowlisted states, counts, versions, and home-relative paths: never tokens,
// keys, MIDs, email addresses, names, message data, or server bodies.

const (
	doctorPass    = "pass"
	doctorWarn    = "warn"
	doctorFail    = "fail"
	doctorSkipped = "skipped"

	clockWarnSkew = time.Minute
	clockFailSkew = 5 * time.Minute
)

type doctorCheck struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Message  string            `json:"message"`
	NextStep string            `json:"next_step,omitempty"`
	Details  map[string]string `json:"details,omitempty"`
}

type doctorReport struct {
	SchemaVersion int            `json:"schema_version"`
	Checks        []doctorCheck  `json:"checks"`
	Summary       map[string]int `json:"summary"`
}

// doctorState carries results between checks without exposing secrets.
type doctorState struct {
	offline         bool
	state           *session.State
	sessionReady    bool
	sessionLockBusy bool
	server, local   time.Time
	clockOK         bool
	authChecked     bool
}

func (a *App) doctorCommand(args []string) error {
	fs := newFlagSet("doctor")
	h := commandHelp{Command: "doctor", Usage: []string{"line doctor [options]"},
		About:    "Check the installation, local session storage, LINE sign-in, encryption keys,\nclock, and watcher. Changes nothing. The report contains no tokens, keys, IDs,\nnames, or messages, so you can paste it into a GitHub issue. Exits 1 only when\na check fails.",
		Groups:   []flagGroup{{"Checks", []string{"offline"}}, {"Output", []string{"json"}}, {"Request", []string{"timeout"}}},
		Examples: []string{"line doctor", "line doctor --offline --json"}}
	offline := fs.Bool("offline", false, "skip network checks (latest release, LINE sign-in, clock)")
	jsonOutput := fs.Bool("json", false, "write the report as JSON")
	timeout := fs.Duration("timeout", 30*time.Second, "command deadline for all checks; 0 disables it")
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; run line doctor --help")
	}
	restore, err := a.commandDeadline(*timeout)
	if err != nil {
		return err
	}
	defer restore()
	a.errorStage = "preparation"
	st := &doctorState{offline: *offline}
	checks := []doctorCheck{a.doctorVersion(st), a.doctorInstall()}
	checks = append(checks, a.doctorSession(st)...)
	checks = append(checks, a.doctorClock(st), a.doctorWatch(st))
	if err := a.loginContext().Err(); err != nil {
		return err
	}
	report := doctorReport{SchemaVersion: 1, Checks: checks, Summary: map[string]int{doctorPass: 0, doctorWarn: 0, doctorFail: 0, doctorSkipped: 0}}
	for _, c := range checks {
		report.Summary[c.Status]++
	}
	if *jsonOutput {
		err = a.json(report)
	} else {
		err = a.renderDoctor(report)
	}
	if err != nil {
		return err
	}
	if report.Summary[doctorFail] > 0 {
		a.errorStage = "completion"
		return fault.New("operation_failed", "completion", fmt.Sprintf("%d doctor check(s) failed; see each check's next step.", report.Summary[doctorFail]), nil)
	}
	return nil
}

func (a *App) renderDoctor(r doctorReport) error {
	if _, err := fmt.Fprintln(a.Out, "LINE CLI doctor"); err != nil {
		return err
	}
	for _, c := range r.Checks {
		marker := map[string]string{doctorPass: "ok  ", doctorWarn: "warn", doctorFail: "FAIL", doctorSkipped: "skip"}[c.Status]
		if _, err := fmt.Fprintf(a.Out, "  [%s] %-9s  %s\n", marker, c.ID, terminalText(c.Message)); err != nil {
			return err
		}
		if c.NextStep != "" {
			if _, err := fmt.Fprintf(a.Out, "                    Next: %s\n", terminalText(c.NextStep)); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(a.Out, "\n%d passed, %d warnings, %d failed, %d skipped.\nThis report has no tokens, keys, IDs, names, or messages; you can paste it into a GitHub issue.\n",
		r.Summary[doctorPass], r.Summary[doctorWarn], r.Summary[doctorFail], r.Summary[doctorSkipped])
	return err
}

func (a *App) doctorVersion(st *doctorState) doctorCheck {
	c := doctorCheck{ID: "version", Status: doctorPass, Details: map[string]string{"version": a.Version, "os": runtime.GOOS, "arch": runtime.GOARCH, "installation": "unknown"}}
	u := a.Updater
	if u == nil {
		u = update.New(a.Version, a.Distribution)
	}
	if inspector, ok := u.(interface{ Inspect() (update.Plan, error) }); ok {
		if p, err := inspector.Inspect(); err == nil && p.Installation != "" {
			c.Details["installation"] = p.Installation
		}
	}
	base := fmt.Sprintf("line %s (%s/%s, %s)", a.Version, runtime.GOOS, runtime.GOARCH, c.Details["installation"])
	if st.offline {
		c.Message = base + "; latest release not checked (--offline)"
		return c
	}
	p, err := u.Check(a.loginContext())
	if err != nil {
		c.Status, c.Message, c.NextStep = doctorWarn, base+"; could not check the latest release", "Check your connection, or run line doctor --offline."
		return c
	}
	if p.Installation != "" {
		c.Details["installation"] = p.Installation
		base = fmt.Sprintf("line %s (%s/%s, %s)", a.Version, runtime.GOOS, runtime.GOARCH, p.Installation)
	}
	c.Details["latest"] = p.LatestVersion
	switch p.Status {
	case "update_available":
		c.Status, c.Message, c.NextStep = doctorWarn, base+"; "+p.LatestVersion+" is available", "Run line update --check for upgrade steps."
	case "up_to_date":
		c.Message = base + "; up to date"
	case "ahead":
		c.Message = base + "; newer than the latest release " + p.LatestVersion
	default:
		c.Message = base + "; latest release is " + p.LatestVersion
	}
	return c
}

func (a *App) doctorInstall() doctorCheck {
	c := doctorCheck{ID: "install", Status: doctorPass}
	if a.Getenv == nil {
		c.Status, c.Message = doctorSkipped, "PATH is unavailable"
		return c
	}
	name := "line"
	if runtime.GOOS == "windows" {
		name = "line.exe"
	}
	found, resolved := findOnPath(a.Getenv("PATH"), name)
	executable := a.Executable
	if executable == nil {
		executable = os.Executable
	}
	running := ""
	if path, err := executable(); err == nil {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			running = real
		} else {
			running = path
		}
	}
	shown := make([]string, len(found))
	for i, path := range found {
		shown[i] = a.homeRelative(path)
	}
	c.Details = map[string]string{"on_path": strings.Join(shown, ", "), "count": strconv.Itoa(len(found))}
	if running != "" {
		c.Details["running"] = a.homeRelative(running)
	}
	switch {
	case len(found) == 0:
		c.Status, c.Message, c.NextStep = doctorWarn, "line is not on PATH", "Add the directory that contains line to PATH."
	case len(found) > 1:
		c.Status = doctorWarn
		c.Message = fmt.Sprintf("%d line executables on PATH; %s runs first", len(found), shown[0])
		c.NextStep = "Remove the copies you don't use, or reorder PATH so the one you update comes first. Others: " + strings.Join(shown[1:], ", ")
	case running != "" && running != resolved[0]:
		c.Status, c.Message = doctorWarn, "This doctor ran "+a.homeRelative(running)+", but line on PATH is "+shown[0]
		c.NextStep = "Run line doctor from PATH, or remove the copy you don't use."
	default:
		c.Message = "One line on PATH: " + shown[0]
	}
	return c
}

// findOnPath lists executables named name in PATH order, once per resolved file.
func findOnPath(pathEnv, name string) (found, resolved []string) {
	seen := make(map[string]bool)
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
			continue
		}
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			real = candidate
		}
		if !seen[real] {
			seen[real] = true
			found, resolved = append(found, candidate), append(resolved, real)
		}
	}
	return found, resolved
}

func (a *App) homeRelative(path string) string {
	if a.Getenv == nil {
		return path
	}
	home := a.Getenv("HOME")
	if runtime.GOOS == "windows" && home == "" {
		home = a.Getenv("USERPROFILE")
	}
	if home == "" || home == string(filepath.Separator) {
		return path
	}
	// Compare with the resolved home too: executables are reported resolved,
	// and HOME may itself contain a symlink (macOS /var -> /private/var).
	homes := []string{home}
	if real, err := filepath.EvalSymlinks(home); err == nil && real != home {
		homes = append(homes, real)
	}
	for _, h := range homes {
		if rel, err := filepath.Rel(h, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return filepath.Join("~", rel)
		}
	}
	return path
}

// doctorSession runs storage, session, LINE sign-in, and key checks under one
// session lock, as other commands do.
func (a *App) doctorSession(st *doctorState) []doctorCheck {
	storage := doctorCheck{ID: "storage", Status: doctorPass}
	saved := doctorCheck{ID: "session", Status: doctorPass}
	auth := doctorCheck{ID: "line_auth", Status: doctorSkipped}
	keys := doctorCheck{ID: "e2ee_keys", Status: doctorSkipped}
	unlock, err := a.lock()
	if err != nil {
		busy := errors.Is(err, session.ErrBusy)
		st.sessionLockBusy = busy
		message, next := "Could not open the session lock", "Run line auth status --check --json and see the storage troubleshooting guide."
		if busy {
			message, next = "Another command is using the session", "Wait for it to finish, then run line doctor again."
		}
		storage.Status, storage.Message, storage.NextStep = doctorWarn, message, next
		saved.Status, saved.Message = doctorSkipped, "Session lock unavailable"
		auth.Message, keys.Message = "Session lock unavailable", "Session lock unavailable"
		return []doctorCheck{storage, saved, auth, keys}
	}
	defer unlock()

	if inspector, ok := a.Manager.Store.(interface {
		Status(bool) (session.StorageStatus, error)
	}); ok {
		status, statusErr := inspector.Status(false)
		storage.Details = map[string]string{"backend": status.Backend, "read_access": status.ReadAccess, "protection": status.Protection, "reason": status.Reason}
		switch {
		case statusErr != nil:
			storage.Details["reason"] = session.StorageReason(statusErr)
			storage.Status = doctorFail
			storage.Message = fmt.Sprintf("Storage %s is not readable (%s)", status.Backend, session.StorageReason(statusErr))
			storage.NextStep = "Run line auth status --check --json and follow the storage troubleshooting guide. Don't delete session files."
		case status.Configured == "none":
			storage.Message = fmt.Sprintf("Storage %s is available; nothing saved yet", status.Backend)
		default:
			storage.Message = fmt.Sprintf("Storage %s is readable", status.Backend)
		}
	} else {
		storage.Status, storage.Message = doctorSkipped, "Storage status is unavailable for this store"
	}

	s, err := a.Manager.Store.Load()
	switch {
	case errors.Is(err, session.ErrNotFound) || err == nil && (s == nil || s.AccessToken == ""):
		saved.Status, saved.Message, saved.NextStep = doctorWarn, "No saved session", "Run line login."
	case err != nil:
		saved.Status, saved.Message = doctorFail, "The saved session could not be read ("+session.StorageReason(err)+")"
		saved.NextStep = "Run line auth status --check --json; restore storage access before logging in again."
	case s.Invalidated:
		saved.Status, saved.Message, saved.NextStep = doctorFail, "The saved session was signed out by LINE", "Run line login."
	default:
		saved.Message = "Saved session found"
		st.state, st.sessionReady = s, true
	}
	if !st.sessionReady {
		auth.Message, keys.Message = "No usable saved session", "No usable saved session"
		return []doctorCheck{storage, saved, auth, keys}
	}
	keys = a.doctorKeys(s)
	if st.offline {
		auth.Message = "Not checked (--offline)"
		return []doctorCheck{storage, saved, auth, keys}
	}
	auth = a.doctorAuth(st)
	return []doctorCheck{storage, saved, auth, keys}
}

func (a *App) doctorAuth(st *doctorState) doctorCheck {
	c := doctorCheck{ID: "line_auth", Status: doctorPass}
	st.authChecked = true
	var profile *line.Profile
	err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
		profile, err = api.GetProfileContext(a.loginContext())
		if clock, ok := api.(interface {
			ResponseClock() (time.Time, time.Time, bool)
		}); ok {
			st.server, st.local, st.clockOK = clock.ResponseClock()
		}
		return err
	})
	switch {
	case err == nil && profile != nil && profile.Mid != "":
		c.Message = "LINE accepted the saved session"
	case err == nil:
		c.Status, c.Message, c.NextStep = doctorWarn, "LINE returned no profile", "Run line doctor again later."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		c.Status, c.Message, c.NextStep = doctorWarn, "LINE did not answer before the deadline", "Check your connection, or raise --timeout."
	case errors.Is(err, session.ErrSessionInvalidated):
		c.Status, c.Message, c.NextStep = doctorFail, "LINE signed this session out", "Run line login."
	case line.IsRefreshRejected(session.ProtocolError(err)):
		c.Status, c.Message, c.NextStep = doctorFail, "LINE rejected the token refresh", "Run line login."
	case line.IsAuthError(session.ProtocolError(err)):
		c.Status, c.Message, c.NextStep = doctorFail, "LINE rejected the saved session", "Run line login if this persists."
	case session.StorageReason(err) != "ok" && session.StorageReason(err) != "storage_error":
		c.Status, c.Message = doctorFail, "Session storage failed during the check ("+session.StorageReason(err)+")"
		c.NextStep = "Run line auth status --check --json."
	default:
		c.Status, c.Message, c.NextStep = doctorWarn, "Could not reach LINE", "Check your connection and try again."
	}
	return c
}

func (a *App) doctorKeys(s *session.State) doctorCheck {
	c := doctorCheck{ID: "e2ee_keys", Status: doctorPass}
	if s.NoE2EE {
		c.Status, c.Message = doctorSkipped, "This account has no Letter Sealing keys"
		return c
	}
	count := 0
	for id, key := range s.ExportedKeys {
		if id != "" && key != "" {
			count++
		}
	}
	c.Details = map[string]string{"count": strconv.Itoa(count)}
	if count == 0 || count != len(s.ExportedKeys) {
		c.Status, c.Message, c.NextStep = doctorFail, "Saved Letter Sealing keys are missing or incomplete", "Run line login to save fresh keys."
		return c
	}
	load := a.LoadKeys
	if load == nil {
		load = func(keys map[string]string) error {
			m, err := e2ee.NewManager()
			if err != nil {
				return err
			}
			return m.LoadMyKeyFromExportedMap(keys)
		}
	}
	if err := load(s.ExportedKeys); err != nil {
		c.Status, c.Message, c.NextStep = doctorFail, fmt.Sprintf("%d saved Letter Sealing key(s) could not be loaded", count), "Run line login to save fresh keys."
		return c
	}
	c.Message = fmt.Sprintf("%d saved Letter Sealing key(s) load", count)
	return c
}

func (a *App) doctorClock(st *doctorState) doctorCheck {
	c := doctorCheck{ID: "clock", Status: doctorSkipped}
	switch {
	case st.offline:
		c.Message = "Not checked (--offline)"
		return c
	case !st.authChecked:
		c.Message = "Needs a LINE response; sign in first"
		return c
	case !st.clockOK:
		c.Message = "LINE's response had no usable Date header"
		return c
	}
	// The Date header has one-second resolution.
	skew := st.local.Sub(st.server)
	if skew < 0 {
		skew = -skew
	}
	skew = max(skew-time.Second, 0).Round(time.Second)
	c.Details = map[string]string{"skew_seconds": strconv.FormatInt(int64(skew/time.Second), 10)}
	switch {
	case skew >= clockFailSkew:
		c.Status, c.Message, c.NextStep = doctorFail, fmt.Sprintf("Local clock differs from LINE by about %s", skew), "Turn on automatic time sync."
	case skew >= clockWarnSkew:
		c.Status, c.Message, c.NextStep = doctorWarn, fmt.Sprintf("Local clock differs from LINE by about %s", skew), "Turn on automatic time sync."
	default:
		c.Status, c.Message = doctorPass, "Local clock matches LINE within a minute"
	}
	return c
}

func (a *App) doctorWatch(st *doctorState) doctorCheck {
	c := doctorCheck{ID: "watch", Status: doctorPass, Details: map[string]string{"lock": "unknown", "checkpoint": "unknown"}}
	checkpoint := ""
	if st.state != nil {
		if st.state.WatchRevision != nil {
			c.Details["checkpoint"], checkpoint = "present", "; resume position saved"
		} else {
			c.Details["checkpoint"], checkpoint = "absent", "; no resume position (the next watch starts now)"
		}
	}
	lock := a.WatchLock
	if lock == nil {
		lock = session.WatchLock
	}
	unlock, err := lock()
	switch {
	case err == nil:
		unlock()
		c.Details["lock"] = "free"
		c.Message = "No watcher is running" + checkpoint
	case errors.Is(err, session.ErrWatchBusy):
		c.Details["lock"] = "held"
		c.Message = "A watcher is running" + checkpoint
	default:
		c.Status, c.Message = doctorWarn, "Could not check the watcher lock"+checkpoint
		c.NextStep = "Run line auth status --check --json to check the configuration directory."
	}
	return c
}
