package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	fn()
	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = stdout, stderr
	var out, errb bytes.Buffer
	io.Copy(&out, outR)
	io.Copy(&errb, errR)
	return out.String(), errb.String()
}

func TestBuildVersion(t *testing.T) {
	prev := version
	t.Cleanup(func() { version = prev })
	version = "v9.9.9"
	if buildVersion() != "v9.9.9" {
		t.Fatalf("stamped: %s", buildVersion())
	}
	version = ""
	if buildVersion() == "" {
		t.Fatal("empty fallback")
	}
}

func TestNeedArg(t *testing.T) {
	if err := needArg([]string{"allow"}, func(string) error { t.Fatal("called"); return nil }); err == nil {
		t.Fatal("missing arg")
	}
	var got string
	if err := needArg([]string{"allow", "ABCD"}, func(s string) error { got = s; return nil }); err != nil || got != "abcd" {
		t.Fatalf("needArg: %q %v", got, err)
	}
}

func TestRunCLIDispatch(t *testing.T) {
	prev := currentEUID
	t.Cleanup(func() { currentEUID = prev })
	currentEUID = func() int { return 0 }
	_, errb := captureOutput(t, func() {
		if code := runCLI(nil); code != 1 {
			t.Fatalf("root code %d", code)
		}
	})
	if !strings.Contains(errb, "don't run hallpass with sudo") {
		t.Fatalf("root stderr: %q", errb)
	}
	currentEUID = func() int { return 1000 }

	out, _ := captureOutput(t, func() {
		if code := runCLI([]string{"--version"}); code != 0 {
			t.Fatalf("version code %d", code)
		}
	})
	if !strings.HasPrefix(out, "hallpass ") {
		t.Fatalf("version: %q", out)
	}
	for _, arg := range []string{"-v", "version", "-h", "--help", "help"} {
		out, errb := captureOutput(t, func() {
			if code := runCLI([]string{arg}); code != 0 {
				t.Fatalf("%s code", arg)
			}
		})
		if arg == "-h" || arg == "--help" || arg == "help" {
			if !strings.Contains(out, "hallpass allow") {
				t.Fatalf("help %s: %q", arg, out)
			}
		}
		_ = errb
	}
	_, errb = captureOutput(t, func() {
		if code := runCLI([]string{"nope"}); code != 2 {
			t.Fatalf("usage code")
		}
	})
	if !strings.Contains(errb, "usage:") {
		t.Fatalf("usage stderr: %q", errb)
	}
	_, errb = captureOutput(t, func() {
		if code := runCLI([]string{"allow"}); code != 1 {
			t.Fatal("allow missing")
		}
	})
	if !strings.Contains(errb, "needs one") {
		t.Fatalf("allow missing: %q", errb)
	}
	_, errb = captureOutput(t, func() {
		if code := runCLI([]string{"revoke", "nope"}); code != 1 {
			t.Fatal("bad revoke")
		}
	})
	if !strings.Contains(errb, "bad id") {
		t.Fatalf("bad id: %q", errb)
	}
}

func TestCLICommands(t *testing.T) {
	root := t.TempDir()
	sys := filepath.Join(root, "sys")
	bus := filepath.Join(root, "bus")
	dev := filepath.Join(root, "dev")
	setGlobal(t, &sysUSB, sys)
	setGlobal(t, &devBusDir, bus)
	setGlobal(t, &devDir, dev)
	rules := filepath.Join(root, "rules")
	os.MkdirAll(rules, 0o755)
	setGlobal(t, &ruleDirs, []string{rules})
	setGlobal(t, &ManagedFile, filepath.Join(root, "70-local-usb.rules"))

	mk := func(dir, name, body string) {
		t.Helper()
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	node := func(rel string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, nil, mode)
	}
	board := filepath.Join(sys, "1-1")
	mk(board, "idVendor", "1234\n")
	mk(board, "idProduct", "5678\n")
	mk(board, "busnum", "1\n")
	mk(board, "devnum", "3\n")
	mk(board, "bDeviceClass", "00\n")
	mk(board, "manufacturer", "Acme\n")
	mk(board, "product", "Widget\n")
	mk(filepath.Join(board, "1-1:1.0", "x", "hidraw", "hidraw0"), "dev", "x")
	node("dev/hidraw0", 0o644)
	node("bus/001/003", 0o644)

	hid := filepath.Join(sys, "2-1")
	mk(hid, "idVendor", "0eee\n")
	mk(hid, "idProduct", "0001\n")
	mk(hid, "busnum", "2\n")
	mk(hid, "devnum", "1\n")
	mk(hid, "bDeviceClass", "00\n")
	mk(hid, "product", "Boot\n")
	mk(filepath.Join(hid, "2-1:1.0", "x", "hidraw", "hidraw1"), "dev", "x")
	node("dev/hidraw1", 0o644)
	node("bus/002/001", 0o444)

	locked := filepath.Join(sys, "4-1")
	mk(locked, "idVendor", "5555\n")
	mk(locked, "idProduct", "6666\n")
	mk(locked, "busnum", "4\n")
	mk(locked, "devnum", "1\n")
	mk(locked, "bDeviceClass", "00\n")
	mk(locked, "product", "Locked\n")
	node("bus/004/001", 0o444)

	hub := filepath.Join(sys, "usb1")
	mk(hub, "idVendor", "1d6b\n")
	mk(hub, "idProduct", "0002\n")
	mk(hub, "busnum", "1\n")
	mk(hub, "devnum", "1\n")
	mk(hub, "bDeviceClass", "09\n")
	mk(hub, "product", "Root Hub XYZ\n")
	node("bus/001/001", 0o644)

	os.WriteFile(filepath.Join(rules, "99-late.rules"), []byte("ATTR{idVendor}==\"0eee\", TAG+=\"uaccess\"\n"), 0o644)
	os.WriteFile(filepath.Join(rules, "70-other.rules"), []byte("ATTR{idVendor}==\"1234\", MODE=\"0660\", GROUP=\"plugdev\"\n"), 0o644)

	stubExec(t, `if [ "$1" = sudo ] && [ "$2" = -n ] && [ "$3" = true ]; then exit 0; fi
exit 0`)

	out, _ := captureOutput(t, func() {
		if err := cmdList(); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "Root Hub XYZ") {
		t.Fatalf("root hub listed: %s", out)
	}
	if !strings.Contains(out, "✓") || !strings.Contains(out, "◐") || !strings.Contains(out, "✗") {
		t.Fatalf("marks: %s", out)
	}
	if !strings.Contains(out, "sorts after "+seatLate) {
		t.Fatalf("late warning missing: %s", out)
	}
	out, _ = captureOutput(t, func() {
		if code := runCLI([]string{"ls"}); code != 0 {
			t.Fatal("ls failed")
		}
	})
	if !strings.Contains(out, "1234:5678") {
		t.Fatalf("ls: %s", out)
	}

	out, _ = captureOutput(t, func() {
		if err := cmdRules(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no grants") {
		t.Fatalf("empty rules: %s", out)
	}
	os.WriteFile(ManagedFile, []byte(Render([]Entry{{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "Acme Widget"}})), 0o644)
	out, _ = captureOutput(t, func() {
		if code := runCLI([]string{"rules"}); code != 0 {
			t.Fatal("rules")
		}
	})
	if !strings.Contains(out, "1234:5678") || !strings.Contains(out, "Acme Widget") {
		t.Fatalf("rules: %s", out)
	}

	var errb string
	out, errb = captureOutput(t, func() {
		if code := runCLI([]string{"allow", "1234:5678"}); code != 0 {
			t.Fatal("allow failed")
		}
	})
	if !strings.Contains(out, "allowed 1234:5678") || !strings.Contains(out, "Acme Widget") {
		t.Fatalf("allow out: %s", out)
	}
	out, errb = captureOutput(t, func() {
		if code := runCLI([]string{"allow", "1234"}); code != 0 {
			t.Fatal("vendor allow failed")
		}
	})
	if !strings.Contains(out, "allowed 1234:*") || !strings.Contains(out, "Test Vendor (all products)") {
		t.Fatalf("vendor allow: %s", out)
	}

	// Empty product string on the device hits the label fallback.
	mk(filepath.Join(sys, "3-1"), "idVendor", "abcd\n")
	mk(filepath.Join(sys, "3-1"), "idProduct", "0001\n")
	mk(filepath.Join(sys, "3-1"), "busnum", "3\n")
	mk(filepath.Join(sys, "3-1"), "devnum", "1\n")
	mk(filepath.Join(sys, "3-1"), "manufacturer", "OnlyMfr\n")
	node("bus/003/001", 0o644)
	out, errb = captureOutput(t, func() {
		if err := cmdAllow("abcd:0001"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "OnlyMfr") {
		t.Fatalf("empty name label: %s %s", out, errb)
	}

	setGlobal(t, &sysUSB, filepath.Join(root, "not-dir"))
	os.WriteFile(sysUSB, []byte("x"), 0o644)
	out, errb = captureOutput(t, func() {
		if err := cmdAllow("1111:2222"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "allowed 1111:2222") {
		t.Fatalf("allow without scan: %s %s", out, errb)
	}
	_, errb = captureOutput(t, func() {
		if code := runCLI([]string{"list"}); code != 1 {
			t.Fatal("list should fail when sysfs is unreadable")
		}
	})
	if !strings.Contains(errb, "hallpass:") {
		t.Fatalf("list err: %s", errb)
	}

	setGlobal(t, &sysUSB, sys)
	stubExec(t, `exit 1`)
	if err := cmdAllow("1234:5678"); err == nil || !strings.Contains(err.Error(), "applying rules") {
		t.Fatalf("sudo failure: %v", err)
	}
	if err := cmdRevoke("1234:5678"); err == nil || !strings.Contains(err.Error(), "applying rules") {
		t.Fatalf("revoke should fail when sudo fails: %v", err)
	}

	stubExec(t, `exit 0`)
	os.WriteFile(ManagedFile, []byte(Render([]Entry{
		{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "A"},
		{Vendor: "0694", Added: "2026-10-04", Label: "B"},
	})), 0o644)
	out, _ = captureOutput(t, func() {
		if err := cmdRevoke("0694:*"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "revoked 0694:*") {
		t.Fatalf("revoke: %s", out)
	}
	out, _ = captureOutput(t, func() {
		if code := runCLI([]string{"rm", "1234:5678"}); code != 0 {
			t.Fatal("rm")
		}
	})
	if !strings.Contains(out, "revoked 1234:5678") {
		t.Fatalf("rm: %s", out)
	}

	os.Remove(ManagedFile)
	out, _ = captureOutput(t, func() {
		if err := cmdRewrite(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no grants") {
		t.Fatalf("empty rewrite: %s", out)
	}
	os.WriteFile(ManagedFile, []byte(Render([]Entry{{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "A"}})), 0o644)
	out, _ = captureOutput(t, func() {
		if code := runCLI([]string{"rewrite"}); code != 0 {
			t.Fatal("rewrite")
		}
	})
	if !strings.Contains(out, "rewrote 1 grant in") {
		t.Fatalf("rewrite one: %s", out)
	}
	os.WriteFile(ManagedFile, []byte(Render([]Entry{
		{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "A"},
		{Vendor: "0694", Added: "2026-10-04", Label: "B"},
	})), 0o644)
	out, _ = captureOutput(t, func() {
		if err := cmdRewrite(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "rewrote 2 grants in") {
		t.Fatalf("rewrite two: %s", out)
	}
}
