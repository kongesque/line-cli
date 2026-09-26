package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestPasswordTerminalRestored(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix PTY test")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed for the stdlib PTY harness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Exercise the actual password code in a child terminal, including input
	// that must never echo. Python's stdlib avoids a runtime PTY dependency.
	cmd := exec.CommandContext(ctx, python, "-c", `
import fcntl, os, pty, select, signal, subprocess, sys, termios, time
for sig in (signal.SIGINT, signal.SIGTERM, "keyboard", None):
    master, slave = pty.openpty()
    before = termios.tcgetattr(master)
    env = dict(os.environ, LINE_CLI_TEST_INPUT_MODE="password")
    def terminal_session():
        os.setsid()
        fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
        os.tcsetpgrp(slave, os.getpgrp())
    child = subprocess.Popen([sys.argv[1], "-test.run=^TestSignalInputHelper$"],
                             stdin=slave, stdout=subprocess.PIPE, stderr=slave, env=env,
                             preexec_fn=terminal_session)
    os.close(slave)
    try:
        output = b""
        deadline = time.monotonic() + 5
        while b"Password: " not in output:
            if time.monotonic() > deadline:
                raise AssertionError("password prompt did not appear")
            if select.select([master], [], [], .1)[0]:
                output += os.read(master, 4096)
        assert not termios.tcgetattr(master)[3] & termios.ECHO, "password echo is enabled"
        os.write(master, b"synthetic-secret" + (b"\n" if sig is None else b""))
        if sig == "keyboard":
            os.write(master, b"\x03")
        elif sig is not None:
            child.send_signal(sig)
        code = child.wait(timeout=5)
        expected = 0 if sig is None else (130 if sig == "keyboard" else 128 + sig)
        assert code == expected, "incorrect signal exit code"
        assert termios.tcgetattr(master) == before, "terminal state was not restored"
        while select.select([master], [], [], .05)[0]:
            try:
                chunk = os.read(master, 4096)
            except OSError:
                break # Linux can report EIO after the controlling tty exits.
            if not chunk:
                break
            output += chunk
        assert b"synthetic-secret" not in output, "password leaked to terminal"
        assert child.stdout.read() == b"", "unexpected success output"
    finally:
        if child.poll() is None:
            child.kill()
            child.wait()
        os.close(master)
`, os.Args[0])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PTY verification failed: %v\n%s", err, out)
	}
}
