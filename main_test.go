package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NarayanaSabari/envkit/internal/store"
)

func TestExportShellRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVKIT_HOME", home)
	t.Setenv("ENVKIT_PROJECT", "cli-test")
	s := store.NewForProject(home, "cli-test")
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	values := []string{
		"plain", "two words", "", "$HOME ${USER}",
		"single'quote and \"double\" quotes", "'edge''quotes'", "back\\slash",
		"$(printf SHELL_EXECUTED)", "`touch SHELL_EXECUTED`",
	}
	var fixture, printArgs strings.Builder
	for i, value := range values {
		fmt.Fprintf(&fixture, "VALUE_%d=%s\n", i, value)
		fmt.Fprintf(&printArgs, " \"$VALUE_%d\"", i)
	}
	if err := os.WriteFile(s.Path(), []byte(fixture.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := os.CreateTemp(t.TempDir(), "export")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	stdout := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = stdout }()
	if err := run([]string{"export"}); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	script, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "eval \"$1\"; printf '%s\\000'"+printArgs.String(), "sh", string(script))
	cmd.Dir = home
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sh: %v: %s", err, got)
	}
	if want := strings.Join(values, "\x00") + "\x00"; string(got) != want {
		t.Fatalf("round trip = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, "SHELL_EXECUTED")); !os.IsNotExist(err) {
		t.Fatalf("shell command executed or marker stat failed: %v", err)
	}
}

func TestReadValuePiped(t *testing.T) {
	for _, input := range []string{"", "two words", "first\nsecond\n\n", "$HOME 'quotes'"} {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err := io.WriteString(w, input); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			stdin := os.Stdin
			os.Stdin = r
			defer func() { os.Stdin = stdin }()
			got, err := readValue("TEST")
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.TrimRight(input, "\n"); got != want {
				t.Fatalf("readValue = %q, want %q", got, want)
			}
		})
	}
}

func TestReadValueHelperProcess(t *testing.T) {
	if os.Getenv("ENVKIT_TEST_READ_VALUE") != "1" {
		return
	}
	value, err := readValue("TEST")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%q", value)
	os.Exit(0)
}

func TestReadValueTerminal(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required for PTY regression")
	}
	stty, err := exec.LookPath("stty")
	if err != nil {
		t.Skip("stty required for terminal input")
	}
	for _, mode := range []string{"line", "empty", "eof", "disable-error"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "disable-error" {
				dir := t.TempDir()
				// Fail after disabling echo to exercise restoration on command errors.
				if err := os.WriteFile(filepath.Join(dir, "stty"), []byte("#!/bin/sh\n\"$ENVKIT_TEST_STTY\" \"$@\" || exit $?\n[ \"$1\" != -echo ]\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("ENVKIT_TEST_STTY", stty)
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, python, "-c", `
import os, pty, subprocess, sys, termios, time
master, slave = pty.openpty()
before = termios.tcgetattr(slave)
proc = subprocess.Popen([sys.argv[1], "-test.run=^TestReadValueHelperProcess$"],
                        stdin=slave, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
try:
    mode = sys.argv[2]
    if mode == "disable-error":
        out, err = proc.communicate(timeout=4)
        assert proc.returncode == 1 and b"exit status 1" in err, (proc.returncode, out, err)
        assert termios.tcgetattr(slave) == before, "terminal settings not restored on error"
        sys.exit(0)
    deadline = time.monotonic() + 4
    while termios.tcgetattr(slave)[3] & termios.ECHO:
        assert proc.poll() is None, "reader exited before hiding input"
        assert time.monotonic() < deadline, "reader did not hide input"
        time.sleep(0.01)
    data = {"line": b"two words $HOME 'quotes'\n", "empty": b"\n", "eof": b"\x04"}[mode]
    os.write(master, data)
    # Master stays open: Enter must finish the read without terminal EOF.
    out, err = proc.communicate(timeout=4)
    assert proc.returncode == 0, (proc.returncode, out, err)
    expected = b'"two words $HOME \'quotes\'"' if mode == "line" else b'""'
    assert out == expected, (out, expected, err)
    assert termios.tcgetattr(slave) == before, "terminal settings not restored"
    assert b"value for TEST: " in err, err
finally:
    if proc.poll() is None:
        proc.kill()
        proc.wait()
    os.close(master)
    os.close(slave)
`, os.Args[0], mode)
			cmd.Env = append(os.Environ(), "ENVKIT_TEST_READ_VALUE=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("PTY regression: %v\n%s", err, out)
			}
		})
	}
}
