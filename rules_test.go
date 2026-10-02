package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderRoundTrip(t *testing.T) {
	in := []Entry{
		{Vendor: "0694", Label: "Lego Group (all products)", Added: "2026-10-02"},
		{Vendor: "0483", Product: "df11", Label: "STM Device in DFU Mode", Added: "2026-10-01"},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "70-local-usb.rules")
	if err := os.WriteFile(path, []byte(Render(in)), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadEntriesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Match() != "0483:df11" || got[1].Match() != "0694:*" || got[1].Label != in[0].Label {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestEntryLines(t *testing.T) {
	l := Entry{Vendor: "05e3", Added: "x", Label: "hub"}.Lines()
	// A vendor grant must not use ATTRS on the usb_device line, or granting
	// a hub vendor would cover everything plugged into that hub.
	if strings.Contains(l[1], "ATTRS") || !strings.Contains(l[1], `ENV{DEVTYPE}=="usb_device"`) {
		t.Fatalf("usb line: %s", l[1])
	}
	if strings.Contains(l[1], "idProduct") {
		t.Fatalf("vendor grant has product: %s", l[1])
	}
}

func TestLate(t *testing.T) {
	for file, late := range map[string]bool{
		"/etc/udev/rules.d/70-lego-hub.rules":  false,
		"/etc/udev/rules.d/72-foo.rules":       false,
		"/etc/udev/rules.d/73-seat-late.rules": true,
		"/etc/udev/rules.d/99-lego-hub.rules":  true,
	} {
		if got := (RuleRef{File: file, Uaccess: true}).Late(); got != late {
			t.Errorf("%s: Late()=%v", file, got)
		}
	}
	if (RuleRef{File: "/x/99-a.rules", Mode: "0666"}).Late() {
		t.Error("MODE-only rule flagged late")
	}
}

func TestIndexFor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "99-x.rules")
	os.WriteFile(path, []byte(`# comment ATTRS{idVendor}=="0694", TAG+="uaccess"
SUBSYSTEM=="usb", ATTRS{idVendor}=="0694", ATTRS{idProduct}=="000[89]|0011", \
  TAG+="uaccess"
SUBSYSTEM=="usb", ATTRS{idVendor}=="1234", MODE="0666"
`), 0o644)
	idx := RuleIndex(scanRuleFile(path))
	refs := idx.For(Device{Vendor: "0694", Product: "0009"})
	if len(refs) != 1 || refs[0].Line != 2 || !refs[0].Late() {
		t.Fatalf("0694:0009: %+v", refs)
	}
	if refs := idx.For(Device{Vendor: "0694", Product: "0010"}); len(refs) != 0 {
		t.Fatalf("0694:0010 should not match: %+v", refs)
	}
	if refs := idx.For(Device{Vendor: "1234", Product: "abcd"}); len(refs) != 1 || refs[0].Mode != "0666" {
		t.Fatalf("1234: %+v", refs)
	}
}

func TestParseID(t *testing.T) {
	for in, want := range map[string]string{"0694": "0694:", "0694:0008": "0694:0008", "0694:*": "0694:"} {
		v, p, err := parseID(in)
		if err != nil || v+":"+p != want {
			t.Errorf("%s -> %s:%s %v", in, v, p, err)
		}
	}
	for _, bad := range []string{"694", "0694:8", "lego", "0694:0008:1"} {
		if _, _, err := parseID(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
