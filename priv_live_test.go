package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestApplyScriptConflict runs the real privileged script against a scratch
// target. It needs passwordless sudo and reloads udev, so it only runs with
// HALLPASS_LIVE=1.
func TestApplyScriptConflict(t *testing.T) {
	if os.Getenv("HALLPASS_LIVE") != "1" {
		t.Skip("set HALLPASS_LIVE=1 to run (uses sudo, reloads udev)")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "70-test.rules")
	staged := filepath.Join(dir, "staged.rules")
	os.WriteFile(target, []byte("old\n"), 0o644)
	os.WriteFile(staged, []byte("new\n"), 0o644)
	run := func(want string) error {
		return exec.Command("sudo", "-n", "sh", "-c", applyScript, "hallpass", staged, target, currentUser(), "", want).Run()
	}

	var ee *exec.ExitError
	if err := run("0000"); !errors.As(err, &ee) || ee.ExitCode() != exitConflict {
		t.Fatalf("stale hash: want exit %d, got %v", exitConflict, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "old\n" {
		t.Fatalf("file written despite conflict: %q", b)
	}
	sum := sha256.Sum256([]byte("old\n"))
	if err := run(hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("matching hash: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new\n" {
		t.Fatalf("not installed: %q", b)
	}
}
