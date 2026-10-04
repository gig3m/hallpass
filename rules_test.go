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
	got, hash, err := loadEntriesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 {
		t.Fatalf("hash %q", hash)
	}
	if len(got) != 2 || got[0].Match() != "0483:df11" || got[1].Match() != "0694:*" || got[1].Label != in[0].Label {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestEntryLines(t *testing.T) {
	l := Entry{Vendor: "05e3", Added: "x", Label: "hub"}.Lines()
	// A grant must match only the device itself. ATTRS walks up through
	// parents, so a hub grant would cover everything plugged into the hub.
	for _, line := range l[1:] {
		if strings.Contains(line, "ATTRS") {
			t.Fatalf("matches ancestors: %s", line)
		}
	}
	if !strings.Contains(l[1], `ENV{DEVTYPE}=="usb_device"`) || !strings.Contains(l[2], `ENV{ID_VENDOR_ID}=="05e3"`) {
		t.Fatalf("lines: %q", l)
	}
	if strings.Contains(l[1], "idProduct") || strings.Contains(l[2], "ID_MODEL_ID") {
		t.Fatalf("vendor grant has product: %q", l)
	}
}

func TestLabelInjection(t *testing.T) {
	evil := "Totally a Keyboard\nSUBSYSTEM==\"usb\", RUN+=\"/bin/sh -c id\"\r\x1b[31m\u2028x"
	d := Device{Vendor: "1234", Product: "5678", Name: evil}
	for _, e := range []Entry{NewEntry(d, false), NewEntry(Device{Vendor: "1234", Manufacturer: evil}, true), {Vendor: "1234", Label: evil}} {
		out := Render([]Entry{e})
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "RUN+=") && !strings.HasPrefix(line, "#") {
				t.Fatalf("injected rule line: %q", line)
			}
			if strings.ContainsAny(line, "\r\x1b\u2028") {
				t.Fatalf("control character survived: %q", line)
			}
		}
		if got := parseEntries([]byte(out)); len(got) != 1 {
			t.Fatalf("round trip: %+v", got)
		}
	}
	if got := cleanText("  a\n\n b\t", 80); got != "a b" {
		t.Fatalf("cleanText: %q", got)
	}
}

func TestValid(t *testing.T) {
	if !(Entry{Vendor: "0694", Added: "2026-10-04"}).Valid() {
		t.Fatal("good entry rejected")
	}
	for _, e := range []Entry{{Vendor: "06\"4"}, {Vendor: "0694", Product: "zz"}, {Vendor: "0694", Added: "x | y"}} {
		if e.Valid() {
			t.Fatalf("bad entry accepted: %+v", e)
		}
	}
}

func TestChangeApply(t *testing.T) {
	cur := []Entry{{Vendor: "0694"}, {Vendor: "03f0", Product: "0fbf"}}
	add := Entry{Vendor: "0a12", Product: "4007"}
	if got, _ := (Change{Add: &add}).apply(cur); len(got) != 3 {
		t.Fatalf("add: %+v", got)
	}
	if got, _ := (Change{Remove: "0694:*"}).apply(cur); len(got) != 1 || got[0].Vendor != "03f0" {
		t.Fatalf("remove: %+v", got)
	}
	if _, err := (Change{Remove: "dead:beef"}).apply(cur); err == nil {
		t.Fatal("removing a missing grant should fail")
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
