package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// stubExec replaces command with a shell script so tests never run sudo,
// udevadm, or loginctl. The script's $1 is the original program name.
func stubExec(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stub.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := command
	command = func(name string, args ...string) *exec.Cmd {
		return exec.Command(path, append([]string{name}, args...)...)
	}
	t.Cleanup(func() { command = prev })
}

func setGlobal[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}
