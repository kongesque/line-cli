package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/internal/update"
	"github.com/kongesque/line-cli/pkg/line"
)

const (
	secretToken = "synthetic-secret-token"
	secretMID   = "u-synthetic-secret-mid"
	secretEmail = "person@example.com"
	secretKey   = "synthetic-secret-key-material"
	secretName  = "Synthetic Secret Name"
)

type statusStore struct {
	testStore
	status    session.StorageStatus
	statusErr error
	loadErr   error
}

func (s *statusStore) Status(bool) (session.StorageStatus, error) { return s.status, s.statusErr }

func (s *statusStore) Load() (*session.State, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.state == nil {
		return nil, session.ErrNotFound
	}
	return s.testStore.Load()
}

type doctorAPI struct {
	session.API
	profileErr    error
	server, local time.Time
	calls         int
}

func (f *doctorAPI) GetProfileContext(context.Context) (*line.Profile, error) {
	f.calls++
	if f.profileErr != nil {
		return nil, f.profileErr
	}
	return &line.Profile{Mid: secretMID, DisplayName: secretName}, nil
}

func (f *doctorAPI) ResponseClock() (time.Time, time.Time, bool) {
	return f.server, f.local, !f.server.IsZero()
}

type doctorUpdater struct {
	plan     update.Plan
	checkErr error
	checks   int
}

func (u *doctorUpdater) Check(context.Context) (update.Plan, error) {
	u.checks++
	return u.plan, u.checkErr
}
func (u *doctorUpdater) Install(context.Context, update.Plan) error {
	panic("doctor installed an update")
}
func (u *doctorUpdater) Inspect() (update.Plan, error) {
	return update.Plan{CurrentVersion: "v0.6.0", Installation: "homebrew"}, nil
}

func fakeExecutable(t *testing.T, dir string) string {
	t.Helper()
	name := "line"
	if runtime.GOOS == "windows" {
		name = "line.exe"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("synthetic"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

type doctorFixture struct {
	app       *App
	out, diag *bytes.Buffer
	store     *statusStore
	api       *doctorAPI
	updater   *doctorUpdater
	watchErr  error
	keysErr   error
	home      string
	path      []string
}

func newDoctorFixture(t *testing.T) *doctorFixture {
	t.Helper()
	home := t.TempDir()
	running := fakeExecutable(t, filepath.Join(home, ".local", "bin"))
	revision := int64(7)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	f := &doctorFixture{
		store: &statusStore{
			testStore: testStore{state: &session.State{Version: 1, AccessToken: secretToken, MID: secretMID, Email: secretEmail, Generation: "g",
				ExportedKeys: map[string]string{"11": secretKey, "12": secretKey}, WatchRevision: &revision}},
			status: session.StorageStatus{Schema: 1, Configured: "present", Backend: "keychain", Email: secretEmail, Protection: "native_os", ReadAccess: "available", Reason: "ok"},
		},
		api:     &doctorAPI{server: now, local: now.Add(1500 * time.Millisecond)},
		updater: &doctorUpdater{plan: update.Plan{CurrentVersion: "v0.6.0", LatestVersion: "v0.6.0", Status: "up_to_date", Installation: "homebrew", Executable: running}},
		home:    home,
		path:    []string{filepath.Dir(running)},
	}
	a, out, diag := testApp(nil)
	a.Manager.Store = f.store
	a.Manager.NewClient = func(string) session.API { return f.api }
	a.Updater = f.updater
	a.Version = "v0.6.0"
	a.Executable = func() (string, error) { return running, nil }
	a.LoadKeys = func(keys map[string]string) error {
		if len(keys) != 2 {
			t.Fatal("unexpected keys")
		}
		return f.keysErr
	}
	a.WatchLock = func() (func(), error) {
		if f.watchErr != nil {
			return nil, f.watchErr
		}
		return func() {}, nil
	}
	a.Getenv = func(name string) string {
		switch name {
		case "PATH":
			return strings.Join(f.path, string(os.PathListSeparator))
		case "HOME", "USERPROFILE":
			return f.home
		}
		return ""
	}
	f.app, f.out, f.diag = a, out, diag
	return f
}

func (f *doctorFixture) report(t *testing.T, args ...string) (doctorReport, map[string]doctorCheck, error) {
	t.Helper()
	f.out.Reset()
	err := f.app.Run(append([]string{"doctor", "--json"}, args...))
	var r doctorReport
	if jsonErr := json.Unmarshal(f.out.Bytes(), &r); jsonErr != nil {
		t.Fatalf("report JSON: %v (%v)", jsonErr, err)
	}
	checks := make(map[string]doctorCheck)
	for _, c := range r.Checks {
		checks[c.ID] = c
	}
	return r, checks, err
}

func requireStatuses(t *testing.T, checks map[string]doctorCheck, want map[string]string) {
	t.Helper()
	for id, status := range want {
		if checks[id].Status != status {
			t.Fatalf("%s: got %s (%q), want %s", id, checks[id].Status, checks[id].Message, status)
		}
		if status != doctorPass && status != doctorSkipped && checks[id].NextStep == "" {
			t.Fatalf("%s has no next step", id)
		}
	}
}

func TestDoctorHealthyReport(t *testing.T) {
	f := newDoctorFixture(t)
	r, checks, err := f.report(t)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(r.Checks))
	for _, c := range r.Checks {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "version,install,storage,session,line_auth,e2ee_keys,clock,watch" || r.SchemaVersion != 1 || r.Summary[doctorPass] != 8 {
		t.Fatalf("report %+v", r)
	}
	requireStatuses(t, checks, map[string]string{"version": doctorPass, "install": doctorPass, "storage": doctorPass, "session": doctorPass, "line_auth": doctorPass, "e2ee_keys": doctorPass, "clock": doctorPass, "watch": doctorPass})
	if checks["e2ee_keys"].Details["count"] != "2" || checks["watch"].Details["checkpoint"] != "present" || checks["watch"].Details["lock"] != "free" ||
		checks["install"].Details["running"] != filepath.Join("~", ".local", "bin", filepath.Base(checks["install"].Details["running"])) || checks["version"].Details["installation"] != "homebrew" {
		t.Fatalf("details %+v", checks)
	}
	f.out.Reset()
	if err := f.app.Run([]string{"--read-only", "doctor"}); err != nil {
		t.Fatal(err)
	}
	if human := f.out.String(); !strings.Contains(human, "[ok  ] line_auth") || !strings.Contains(human, "8 passed, 0 warnings, 0 failed, 0 skipped.") {
		t.Fatalf("human report %q", human)
	}
}

func TestDoctorNeverPrintsSecrets(t *testing.T) {
	f := newDoctorFixture(t)
	f.store.statusErr = errors.New("storage detail " + secretToken)
	f.api.profileErr = errors.New("server body " + secretName)
	for _, args := range [][]string{{"doctor", "--json"}, {"doctor"}, {"--error-format=json", "doctor", "--json"}} {
		f.out.Reset()
		f.diag.Reset()
		_ = f.app.Run(args)
		all := f.out.String() + f.diag.String()
		for _, secret := range []string{secretToken, secretMID, secretEmail, secretKey, secretName, f.home} {
			if strings.Contains(all, secret) {
				t.Fatalf("%q leaked %q in %q", args, secret, all)
			}
		}
	}
}

func TestDoctorLoggedOutAndOffline(t *testing.T) {
	f := newDoctorFixture(t)
	f.store.state = nil
	f.store.status = session.StorageStatus{Configured: "none", Backend: "headless", ReadAccess: "not_applicable", Reason: "no_session"}
	f.app.Manager.NewClient = func(string) session.API { t.Fatal("logged-out doctor contacted LINE"); return nil }
	_, checks, err := f.report(t)
	if err != nil {
		t.Fatal(err)
	}
	requireStatuses(t, checks, map[string]string{"storage": doctorPass, "session": doctorWarn, "line_auth": doctorSkipped, "e2ee_keys": doctorSkipped, "clock": doctorSkipped, "watch": doctorPass})
	if !strings.Contains(checks["storage"].Message, "headless") || checks["watch"].Details["checkpoint"] != "unknown" {
		t.Fatalf("logged out %+v", checks)
	}

	f = newDoctorFixture(t)
	f.app.Manager.NewClient = func(string) session.API { t.Fatal("offline doctor contacted LINE"); return nil }
	_, checks, err = f.report(t, "--offline")
	if err != nil || f.updater.checks != 0 {
		t.Fatalf("offline: %v checks=%d", err, f.updater.checks)
	}
	requireStatuses(t, checks, map[string]string{"version": doctorPass, "line_auth": doctorSkipped, "clock": doctorSkipped, "e2ee_keys": doctorPass})
	if !strings.Contains(checks["version"].Message, "not checked (--offline)") || checks["version"].Details["installation"] != "homebrew" {
		t.Fatalf("offline version %+v", checks["version"])
	}
}

func TestDoctorFailuresExitNonzero(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*doctorFixture)
		want  map[string]string
	}{
		"invalidated": {func(f *doctorFixture) { f.store.state.Invalidated = true }, map[string]string{"session": doctorFail, "line_auth": doctorSkipped}},
		"storage":     {func(f *doctorFixture) { f.store.statusErr = session.ErrStorageFormat }, map[string]string{"storage": doctorFail}},
		"keys":        {func(f *doctorFixture) { f.keysErr = errors.New("bad key") }, map[string]string{"e2ee_keys": doctorFail}},
		"missing keys": {func(f *doctorFixture) { f.store.state.ExportedKeys = map[string]string{"11": ""} },
			map[string]string{"e2ee_keys": doctorFail}},
		"clock": {func(f *doctorFixture) { f.api.local = f.api.server.Add(-10 * time.Minute) }, map[string]string{"clock": doctorFail}},
		"auth":  {func(f *doctorFixture) { f.api.profileErr = &line.ResponseError{Status: 401} }, map[string]string{"line_auth": doctorFail}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDoctorFixture(t)
			tc.setup(f)
			_, checks, err := f.report(t)
			if err == nil {
				t.Fatal("failed check exited 0")
			}
			requireStatuses(t, checks, tc.want)
		})
	}
	f := newDoctorFixture(t)
	f.store.state.Invalidated = true
	f.out.Reset()
	err := f.app.Run([]string{"--error-format=json", "doctor", "--json"})
	var machine *MachineError
	if !errors.As(err, &machine) || machine.Detail.Code != "operation_failed" || machine.Detail.Stage != "completion" || !strings.Contains(f.out.String(), `"schema_version": 1`) {
		t.Fatalf("machine error %v", err)
	}
}

func TestDoctorWarnings(t *testing.T) {
	f := newDoctorFixture(t)
	f.api.local = f.api.server.Add(2 * time.Minute)
	f.api.profileErr = nil
	f.updater.plan.Status, f.updater.plan.LatestVersion = "update_available", "v0.6.1"
	f.watchErr = session.ErrWatchBusy
	old := fakeExecutable(t, filepath.Join(t.TempDir(), "brew", "bin"))
	f.path = append(f.path, filepath.Dir(old), filepath.Dir(old))
	_, checks, err := f.report(t)
	if err != nil {
		t.Fatalf("warnings must exit 0: %v", err)
	}
	requireStatuses(t, checks, map[string]string{"version": doctorWarn, "install": doctorWarn, "clock": doctorWarn, "watch": doctorPass})
	if checks["install"].Details["count"] != "2" || !strings.Contains(checks["install"].NextStep, old) || checks["watch"].Details["lock"] != "held" {
		t.Fatalf("warnings %+v", checks)
	}

	f = newDoctorFixture(t)
	f.app.Lock = func() (func(), error) { return nil, session.ErrBusy }
	f.updater.checkErr = errors.New("offline")
	f.path = nil
	_, checks, err = f.report(t)
	if err != nil {
		t.Fatal(err)
	}
	requireStatuses(t, checks, map[string]string{"version": doctorWarn, "install": doctorWarn, "storage": doctorWarn, "session": doctorSkipped, "line_auth": doctorSkipped})
	f = newDoctorFixture(t)
	f.api.profileErr = errors.New("connection refused")
	_, checks, _ = f.report(t)
	requireStatuses(t, checks, map[string]string{"line_auth": doctorWarn, "clock": doctorPass})
}

func TestDoctorValidation(t *testing.T) {
	for _, args := range [][]string{{"doctor", "extra"}, {"doctor", "--bogus"}, {"doctor", "--timeout", "-1s"}} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("opened session for invalid arguments"); return nil, nil }
		if err := a.Run(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}
