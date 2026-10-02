package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ManagedFile is the only file hallpass writes. It must sort before
// 73-seat-late.rules, or TAG+="uaccess" is applied but no ACL is granted.
const ManagedFile = "/etc/udev/rules.d/70-local-usb.rules"

const seatLate = "73-seat-late.rules"

var ruleDirs = []string{"/etc/udev/rules.d", "/run/udev/rules.d", "/usr/lib/udev/rules.d", "/lib/udev/rules.d"}

// Entry is one grant in the managed file: a vendor, optionally one product.
type Entry struct {
	Vendor  string
	Product string // "" = every product from this vendor
	Label   string
	Added   string // YYYY-MM-DD
}

func (e Entry) Match() string {
	if e.Product == "" {
		return e.Vendor + ":*"
	}
	return e.Vendor + ":" + e.Product
}

func (e Entry) Covers(d Device) bool {
	return e.Vendor == d.Vendor && (e.Product == "" || e.Product == d.Product)
}

// Lines renders the entry: the raw USB node (WebUSB, libusb, DFU tools) and
// its hidraw nodes (WebHID). ATTR, not ATTRS, on the usb_device line so a
// vendor grant for a hub doesn't spill onto everything plugged into it.
func (e Entry) Lines() []string {
	usb := fmt.Sprintf(`SUBSYSTEM=="usb", ENV{DEVTYPE}=="usb_device", ATTR{idVendor}=="%s"`, e.Vendor)
	hid := fmt.Sprintf(`SUBSYSTEM=="hidraw", KERNEL=="hidraw*", ATTRS{idVendor}=="%s"`, e.Vendor)
	if e.Product != "" {
		usb += fmt.Sprintf(`, ATTR{idProduct}=="%s"`, e.Product)
		hid += fmt.Sprintf(`, ATTRS{idProduct}=="%s"`, e.Product)
	}
	return []string{
		fmt.Sprintf("# hallpass: %s | %s | %s", e.Match(), e.Added, e.Label),
		usb + `, TAG+="uaccess"`,
		hid + `, TAG+="uaccess"`,
	}
}

func NewEntry(d Device, wholeVendor bool) Entry {
	e := Entry{Vendor: d.Vendor, Product: d.Product, Label: d.Label(), Added: time.Now().Format("2006-01-02")}
	if wholeVendor {
		e.Product = ""
		e.Label = vendorLabel(d)
	}
	return e
}

func vendorLabel(d Device) string {
	if v := usbIDs().vendor(d.Vendor); v != "" {
		return v + " (all products)"
	}
	if d.Manufacturer != "" {
		return d.Manufacturer + " (all products)"
	}
	return "vendor " + d.Vendor + " (all products)"
}

var headerRe = regexp.MustCompile(`^# hallpass: ([0-9a-f]{4}):([0-9a-f]{4}|\*) \| (\S*) \| (.*)$`)

// LoadEntries reads the managed file. A missing file is no entries.
func LoadEntries() ([]Entry, error) { return loadEntriesFrom(ManagedFile) }

func loadEntriesFrom(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := headerRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		e := Entry{Vendor: m[1], Product: m[2], Added: m[3], Label: m[4]}
		if e.Product == "*" {
			e.Product = ""
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Render produces the whole managed file. Entries are regenerated from their
// headers, so the file is never hand-merged.
func Render(entries []Entry) string {
	entries = append([]Entry(nil), entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Match() < entries[j].Match() })
	var b strings.Builder
	b.WriteString("# Managed by hallpass (github.com/gig3m/hallpass). Edit with `hallpass`, not by hand.\n")
	b.WriteString("# Grants the logged-in seat user access via uaccess. This file must sort before\n")
	b.WriteString("# " + seatLate + " or the uaccess tag is applied but no ACL is granted.\n")
	for _, e := range entries {
		b.WriteString("\n")
		for _, l := range e.Lines() {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// WithEntry adds e, replacing any entry with the same match.
func WithEntry(entries []Entry, e Entry) []Entry {
	out := []Entry{e}
	for _, x := range entries {
		if x.Match() != e.Match() {
			out = append(out, x)
		}
	}
	return out
}

func WithoutEntry(entries []Entry, match string) []Entry {
	var out []Entry
	for _, x := range entries {
		if x.Match() != match {
			out = append(out, x)
		}
	}
	return out
}

// RuleRef is a line in some udev rules file that grants access to a device.
type RuleRef struct {
	File    string // full path
	Line    int
	Uaccess bool
	Mode    string
	Group   string
}

func (r RuleRef) Base() string { return filepath.Base(r.File) }

// Late is true for a uaccess grant that runs after 73-seat-late.rules and so
// never produces an ACL. This is the silent failure hallpass exists to catch.
func (r RuleRef) Late() bool { return r.Uaccess && r.Base() >= seatLate }

func (r RuleRef) String() string {
	var what []string
	if r.Uaccess {
		what = append(what, "uaccess")
	}
	if r.Mode != "" {
		what = append(what, "MODE="+r.Mode)
	}
	if r.Group != "" {
		what = append(what, "GROUP="+r.Group)
	}
	return fmt.Sprintf("%s:%d %s", r.Base(), r.Line, strings.Join(what, " "))
}

var (
	vidRe   = regexp.MustCompile(`ATTRS?\{idVendor\}=="([^"]*)"`)
	pidRe   = regexp.MustCompile(`ATTRS?\{idProduct\}=="([^"]*)"`)
	modeRe  = regexp.MustCompile(`MODE:?="([^"]*)"`)
	groupRe = regexp.MustCompile(`GROUP:?="([^"]*)"`)
)

type ruleLine struct {
	file, text string
	line       int
}

// RuleIndex is every access-granting line across the udev rule dirs, with
// /etc overriding same-named files in /run and /usr/lib like udev does.
type RuleIndex []ruleLine

func LoadRuleIndex() RuleIndex {
	seen := map[string]bool{}
	var idx RuleIndex
	for _, dir := range ruleDirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.rules"))
		for _, path := range files {
			base := filepath.Base(path)
			if seen[base] {
				continue
			}
			seen[base] = true
			if real, err := filepath.EvalSymlinks(path); err == nil && real == os.DevNull {
				continue // masked
			}
			idx = append(idx, scanRuleFile(path)...)
		}
	}
	return idx
}

func scanRuleFile(path string) []ruleLine {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []ruleLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n, start := 0, 0
	var cur strings.Builder
	for sc.Scan() {
		n++
		text := sc.Text()
		if cur.Len() == 0 {
			start = n
		}
		if strings.HasSuffix(text, "\\") {
			cur.WriteString(strings.TrimSuffix(text, "\\"))
			continue
		}
		cur.WriteString(text)
		line := strings.TrimSpace(cur.String())
		cur.Reset()
		if line == "" || line[0] == '#' || !strings.Contains(line, "idVendor") {
			continue
		}
		if strings.Contains(line, `"uaccess"`) || modeRe.MatchString(line) || groupRe.MatchString(line) {
			out = append(out, ruleLine{file: path, text: line, line: start})
		}
	}
	return out
}

// For returns rule lines whose idVendor/idProduct match d. Matching is
// pattern-only: it ignores SUBSYSTEM/KERNEL/ENV conditions, so it can list a
// rule that targets a different node of the same device (e.g. hidraw).
func (idx RuleIndex) For(d Device) []RuleRef {
	var out []RuleRef
	for _, r := range idx {
		vm := vidRe.FindStringSubmatch(r.text)
		if vm == nil || !globMatch(vm[1], d.Vendor) {
			continue
		}
		if pm := pidRe.FindStringSubmatch(r.text); pm != nil && !globMatch(pm[1], d.Product) {
			continue
		}
		ref := RuleRef{File: r.file, Line: r.line, Uaccess: strings.Contains(r.text, `"uaccess"`)}
		if m := modeRe.FindStringSubmatch(r.text); m != nil {
			ref.Mode = m[1]
		}
		if m := groupRe.FindStringSubmatch(r.text); m != nil {
			ref.Group = m[1]
		}
		out = append(out, ref)
	}
	return out
}

// globMatch handles udev's value syntax: shell globs with | alternatives.
func globMatch(pattern, value string) bool {
	for _, alt := range strings.Split(strings.ToLower(pattern), "|") {
		if ok, _ := filepath.Match(alt, value); ok {
			return true
		}
	}
	return false
}
