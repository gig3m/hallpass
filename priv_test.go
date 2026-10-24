package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestCurrentUserAndSudoReady(t *testing.T) {
	if currentUser() == "" {
		t.Fatal("empty user")
	}
	stubExec(t, `if [ "$1" = sudo ] && [ "$2" = -n ] && [ "$3" = true ]; then exit 0; fi
exit 1`)
	if !SudoReady() {
		t.Fatal("stub should be ready")
	}
	stubExec(t, `exit 1`)
	if SudoReady() {
		t.Fatal("stub should not be ready")
	}
}

func TestSeat(t *testing.T) {
	t.Setenv("XDG_SEAT", "")
	t.Setenv("XDG_SESSION_ID", "")
	if seat, active := Seat(); seat != "" || active {
		t.Fatalf("empty: %q %v", seat, active)
	}
	t.Setenv("XDG_SEAT", "seat0")
	if seat, active := Seat(); seat != "seat0" || active {
		t.Fatalf("no session: %q %v", seat, active)
	}
	stubExec(t, `if [ "$1" = loginctl ]; then printf '%s\n' "$STUB_OUT"; exit ${STUB_EXIT:-0}; fi
exit 1`)
	t.Setenv("XDG_SESSION_ID", "1")
	t.Setenv("STUB_OUT", "yes")
	t.Setenv("STUB_EXIT", "0")
	if seat, active := Seat(); seat != "seat0" || !active {
		t.Fatalf("active: %q %v", seat, active)
	}
	t.Setenv("STUB_OUT", "no")
	if _, active := Seat(); active {
		t.Fatal("inactive session")
	}
	t.Setenv("STUB_OUT", " yes ")
	if _, active := Seat(); !active {
		t.Fatal("trimmed yes")
	}
	t.Setenv("STUB_EXIT", "1")
	t.Setenv("STUB_OUT", "yes")
	if _, active := Seat(); active {
		t.Fatal("loginctl failure")
	}
}

func splitApply(t *testing.T, args []string) (staged, target, user, strip, hash string, vendors []string) {
	t.Helper()
	for i, a := range args {
		if a == "hallpass" && i+6 <= len(args) {
			return args[i+1], args[i+2], args[i+3], args[i+4], args[i+5], args[i+6:]
		}
	}
	t.Fatalf("no hallpass sentinel in %q", args)
	return "", "", "", "", "", nil
}

func TestApplyCmd(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/70-local-usb.rules"
	setGlobal(t, &ManagedFile, path)

	bad := Entry{Vendor: "zzzz", Product: "0001", Added: "2026-01-01"}
	if _, cleanup, err := ApplyCmd(Change{Add: &bad}, nil); err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("invalid add accepted")
	}

	if _, _, err := ApplyCmd(Change{Remove: "0694:*"}, []string{"-n"}); err == nil {
		t.Fatal("remove missing should fail")
	}

	os.WriteFile(path, []byte("# hallpass: 0694:* | not-a-date | Bad\n"), 0o644)
	add := Entry{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "ok"}
	if _, _, err := ApplyCmd(Change{Add: &add}, nil); err == nil {
		t.Fatal("invalid existing entry should fail the apply")
	}

	good := Render([]Entry{
		{Vendor: "0694", Added: "2026-10-01", Label: "Lego"},
		{Vendor: "03f0", Product: "0fbf", Added: "2026-10-02", Label: "HP"},
	})
	os.WriteFile(path, []byte(good), 0o644)
	sum := sha256.Sum256([]byte(good))
	wantHash := hex.EncodeToString(sum[:])

	cmd, cleanup, err := ApplyCmd(Change{Remove: "0694:*"}, []string{"-n"})
	if err != nil {
		t.Fatal(err)
	}
	staged, target, user, strip, hash, vendors := splitApply(t, cmd.Args)
	if target != path || user != currentUser() || strip != "0694:" || hash != wantHash {
		t.Fatalf("remove args: target %s user %s strip %s hash %s", target, user, strip, hash)
	}
	if len(vendors) != 1 || vendors[0] != "0694" {
		t.Fatalf("remove vendors: %q", vendors)
	}
	body, err := os.ReadFile(staged)
	if err != nil || strings.Contains(string(body), "0694") {
		t.Fatalf("staged still has revoked vendor: %s %v", body, err)
	}
	if !strings.Contains(strings.Join(cmd.Args, "\n"), "@@ install") || cmd.Args[0] != "sudo" {
		t.Fatalf("sudo args: %q", cmd.Args)
	}
	cleanup()
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("cleanup left the staged file")
	}

	one := Entry{Vendor: "0a12", Product: "4007", Added: "2026-10-04", Label: "csr"}
	cmd, cleanup, err = ApplyCmd(Change{Add: &one}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	staged, _, _, strip, _, vendors = splitApply(t, cmd.Args)
	if strip != "" {
		t.Fatalf("add strip: %q", strip)
	}
	body, err = os.ReadFile(staged)
	if err != nil || !strings.Contains(string(body), "0a12") || !strings.Contains(string(body), "03f0") {
		t.Fatalf("staged add: %s %v", body, err)
	}
	found := map[string]bool{}
	for _, v := range vendors {
		found[v] = true
	}
	if !found["0a12"] || len(vendors) != 1 {
		t.Fatalf("add retriggers the new vendor only: %q", vendors)
	}

	cmd, cleanup, err = ApplyCmd(Change{}, []string{"-n"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, _, _, strip, _, vendors = splitApply(t, cmd.Args)
	if strip != "" {
		t.Fatalf("rewrite strip: %q", strip)
	}
	found = map[string]bool{}
	for _, v := range vendors {
		found[v] = true
	}
	if !found["0694"] || !found["03f0"] || found["0a12"] {
		t.Fatalf("rewrite vendors: %q", vendors)
	}

	os.Remove(path)
	cmd, cleanup, err = ApplyCmd(Change{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	staged, _, _, _, hash, vendors = splitApply(t, cmd.Args)
	if staged != "" || hash != "" || len(vendors) != 0 {
		t.Fatalf("empty rewrite: staged %q hash %q vendors %q", staged, hash, vendors)
	}

	setGlobal(t, &ManagedFile, t.TempDir())
	if _, _, err := ApplyCmd(Change{}, nil); err == nil {
		t.Fatal("unreadable rules path should fail")
	}
}
