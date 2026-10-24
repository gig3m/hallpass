package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func runeKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(m model, k tea.KeyMsg) (model, tea.Cmd) {
	nm, cmd := m.Update(k)
	return nm.(model), cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func baseModel() model {
	m := model{
		known:      map[string]Device{},
		user:       "ada",
		seat:       "seat0",
		seatActive: true,
		sudoCached: true,
		statusC:    cDim,
		w:          120,
		h:          40,
	}
	return m
}

func devAt(sys, vid, pid, name, class string, writable bool, hid []Node) Device {
	return Device{
		SysName: sys, Vendor: vid, Product: pid, Name: name, Manufacturer: "Acme",
		Class: class, Bus: 1, Dev: 4, Devnode: "/dev/bus/usb/001/004",
		Writable: writable, Owner: "root:root 0664", Serial: "SN1", Hidraw: hid,
	}
}

func TestRenderHelpers(t *testing.T) {
	if fit("hi", 4) != "hi  " {
		t.Fatalf("pad: %q", fit("hi", 4))
	}
	if !strings.Contains(fit("abcdef", 4), "…") && lipWidth(fit("abcdef", 4)) > 4 {
		t.Fatalf("truncate: %q", fit("abcdef", 4))
	}
	if chip("a") == "" || !strings.Contains(renderSegs([]seg{{t: "hello", c: cFg}}, 0, "", false), "hello") {
		t.Fatal("chip/segs")
	}
	truncated := renderSegs([]seg{{t: "abcdefghij", c: cFg, w: 20}}, 6, cSel, true)
	if lipWidth(truncated) > 6 {
		t.Fatalf("seg width: %q", truncated)
	}
	padded := renderSegs([]seg{{t: "x", c: cFg}}, 5, "", false)
	if !strings.Contains(padded, "x") {
		t.Fatal("pad no bg")
	}
	bg := renderSegs([]seg{{t: "x", c: cFg}}, 5, cSel, false)
	if !strings.Contains(bg, "x") {
		t.Fatal("pad bg")
	}

	boxed := box("title", "meta", []string{strings.Repeat("z", 80)}, 20, 4, cBrand, cBrand)
	if len(boxed) != 4 || !strings.Contains(boxed[0], "title") {
		t.Fatalf("box: %q", boxed)
	}
	short := box("t", "", nil, 10, 1, cLine, cDim)
	if len(short) != 2 {
		t.Fatalf("short box: %d", len(short))
	}

	bgLines := []string{strings.Repeat("a", 30), strings.Repeat("b", 10)}
	over := overlay(bgLines, []string{"HELLO", "THERE"}, 30)
	if len(over) != 2 || !strings.Contains(over[0], "HELLO") {
		t.Fatalf("overlay: %q", over)
	}
	wide := overlay([]string{"ab"}, []string{"LONGER THAN BG"}, 2)
	if len(wide) != 1 {
		t.Fatal("wide overlay")
	}
	tall := overlay([]string{"only"}, []string{"one", "two", "three"}, 10)
	if !strings.Contains(tall[0], "one") {
		t.Fatalf("tall overlay: %q", tall)
	}

	if got := wrap("", 10); len(got) != 0 {
		t.Fatal("empty wrap")
	}
	if got := wrap("abcdefghij", 3); len(got) != 1 || got[0] != "abcdefghij" {
		t.Fatalf("long word: %q", got)
	}
	if got := wrap("aa bb cc", 5); len(got) < 2 {
		t.Fatalf("wrap words: %q", got)
	}
	rows := wrapSegs([]seg{{t: "abcdef", c: cFg}, {t: "gh", c: cBad}}, 4)
	if len(rows) < 2 {
		t.Fatalf("wrapSegs: %+v", rows)
	}
	body := pinBottom([]string{"a", "b", "c", "d"}, []string{"foot"}, 3)
	if len(body) != 3 || body[2] != "foot" {
		t.Fatalf("pin: %q", body)
	}
	if spread("l", "r", 4) == "" || orDash("") != "—" || orDash("s") != "s" {
		t.Fatal("spread/orDash")
	}
	if plural(1) != "" || plural(2) != "s" || pluralES(1) != "" || pluralES(2) != "es" {
		t.Fatal("plural")
	}
	if baseName("a/b") != "b" || baseName("b") != "b" || lastLine("a\nb\n") != "b" || lastLine("") != "" {
		t.Fatal("base/last")
	}
	s, e := window(10, 0, 3)
	if s != 0 || e != 3 {
		t.Fatalf("window top: %d %d", s, e)
	}
	s, e = window(10, 8, 3)
	if s != 6 || e != 9 {
		t.Fatalf("window mid: %d %d", s, e)
	}
	if _, end := window(2, 0, 0); end < 1 {
		t.Fatal("window min height")
	}

	open, c := statusMark(Device{Writable: true})
	hid, _ := statusMark(Device{Hidraw: []Node{{Writable: true}}})
	locked, _ := statusMark(Device{})
	if open != "✓" || hid != "◐" || locked != "✗" || c == "" {
		t.Fatalf("marks %s %s %s", open, hid, locked)
	}
	if header(10, seg{t: "ID", w: 4}) == "" {
		t.Fatal("header")
	}
	allow := job{allow: true, entry: Entry{Vendor: "1234", Product: "5678"}}
	if allow.change().Add == nil || allow.change().Remove != "" {
		t.Fatal("allow change")
	}
	revoke := job{entry: Entry{Vendor: "1234"}}
	if revoke.change().Remove != "1234:*" || verb(allow) != "issuing" || verb(revoke) != "revoking" {
		t.Fatal("revoke change")
	}
}

func lipWidth(s string) int {
	return len([]rune(stripANSI(s)))
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestModelLists(t *testing.T) {
	m := baseModel()
	m.all = []Device{
		devAt("usb1", "1d6b", "0002", "Root", "09", true, nil),
		devAt("1-1", "1234", "5678", "Widget", "00", true, []Node{{Path: "/dev/hidraw0", Writable: true}}),
		devAt("1-2", "2222", "3333", "Hub", "09", false, nil),
	}
	m.entries = []Entry{{Vendor: "9999", Product: "0001", Label: "later"}, {Vendor: "1234", Product: "5678", Label: "first"}}
	sortEntries(m.entries)
	if m.entries[0].Vendor != "1234" {
		t.Fatalf("sort: %+v", m.entries)
	}
	m.filter()
	if len(m.view) != 1 || m.view[0].SysName != "1-1" {
		t.Fatalf("hubs hidden: %+v", m.view)
	}
	if m.hubCount() != 1 {
		t.Fatal("hub count")
	}
	m.showHubs = true
	m.cur[0] = 0
	m.filter()
	if len(m.view) != 2 {
		t.Fatalf("hubs shown: %d", len(m.view))
	}
	if _, ok := m.selected(); !ok {
		t.Fatal("selected")
	}
	m.view = nil
	m.cur[0] = 5
	m.clamp()
	if _, ok := m.selected(); ok {
		t.Fatal("empty selection")
	}
	if _, ok := m.entryFor(m.all[1]); !ok {
		t.Fatal("product entry")
	}
	m.entries = []Entry{{Vendor: "1234", Label: "vendor"}}
	if e, ok := m.entryFor(m.all[1]); !ok || e.Product != "" {
		t.Fatal("vendor entry")
	}
	if n := m.plugged(m.entries[0]); n != 1 {
		t.Fatalf("plugged %d", n)
	}
	if !strings.Contains(m.quitLine(), "1 pass in") {
		t.Fatal(m.quitLine())
	}
	m.entries = append(m.entries, Entry{Vendor: "aaaa"})
	if !strings.Contains(m.quitLine(), "passes") {
		t.Fatal(m.quitLine())
	}
	for i := 0; i < 6; i++ {
		m.logEvent(event{at: time.Now(), add: i%2 == 0, d: m.all[1], note: "n"})
	}
	if len(m.events) != maxEvents {
		t.Fatalf("events %d", len(m.events))
	}
	if m.mainH() < 8 || !m.showCard() || m.listTop() != 4 || m.listRows() < 1 || !m.modal() && m.help {
		t.Fatal("layout")
	}
	m.w = 80
	if m.showCard() {
		t.Fatal("narrow hides card")
	}
	m.tab = 1
	if m.listTop() != 6 {
		t.Fatal("grants list top")
	}
	m.help = true
	if !m.modal() {
		t.Fatal("help is modal")
	}
}

func TestRescanAndReload(t *testing.T) {
	root := t.TempDir()
	sys := root + "/sys"
	setGlobal(t, &sysUSB, sys)
	setGlobal(t, &devBusDir, root+"/bus")
	setGlobal(t, &devDir, root+"/dev")
	setGlobal(t, &ManagedFile, root+"/70-local-usb.rules")
	setGlobal(t, &ruleDirs, []string{root + "/rules"})

	mustWrite(t, sys+"/old/idVendor", "1111\n")
	mustWrite(t, sys+"/old/idProduct", "2222\n")
	mustWrite(t, sys+"/old/busnum", "1\n")
	mustWrite(t, sys+"/old/devnum", "9\n")
	mustWrite(t, sys+"/old/product", "Gone\n")
	mustWrite(t, sys+"/usb1/idVendor", "1d6b\n")
	mustWrite(t, sys+"/usb1/idProduct", "0002\n")
	mustWrite(t, sys+"/usb1/busnum", "1\n")
	mustWrite(t, sys+"/usb1/devnum", "1\n")
	mustWrite(t, sys+"/usb1/bDeviceClass", "09\n")
	mustWrite(t, sys+"/usb1/product", "Hub\n")

	m := baseModel()
	m.rescan()
	if !m.scanned || len(m.events) != 0 {
		t.Fatalf("first scan events: %+v", m.events)
	}
	// Drop "old", add a locked device, a writable device, and one with a pass.
	if err := os.RemoveAll(sys + "/old"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, sys+"/1-1/idVendor", "1234\n")
	mustWrite(t, sys+"/1-1/idProduct", "5678\n")
	mustWrite(t, sys+"/1-1/busnum", "1\n")
	mustWrite(t, sys+"/1-1/devnum", "3\n")
	mustWrite(t, sys+"/1-1/product", "Passed\n")
	mustWrite(t, sys+"/1-2/idVendor", "2222\n")
	mustWrite(t, sys+"/1-2/idProduct", "3333\n")
	mustWrite(t, sys+"/1-2/busnum", "1\n")
	mustWrite(t, sys+"/1-2/devnum", "4\n")
	mustWrite(t, sys+"/1-2/product", "Locked\n")
	mustWrite(t, sys+"/1-3/idVendor", "3333\n")
	mustWrite(t, sys+"/1-3/idProduct", "4444\n")
	mustWrite(t, sys+"/1-3/busnum", "1\n")
	mustWrite(t, sys+"/1-3/devnum", "5\n")
	mustWrite(t, sys+"/1-3/product", "Open\n")
	mustWrite(t, root+"/bus/001/005", "")
	if err := os.Chmod(root+"/bus/001/005", 0o644); err != nil {
		t.Fatal(err)
	}
	m.entries = []Entry{{Vendor: "1234", Product: "5678", Label: "pass"}}
	m.rescan()
	notes := map[string]string{}
	for _, e := range m.events {
		notes[e.d.SysName] = e.note
	}
	if notes["old"] != "" || notes["1-1"] != "pass ✓" || notes["1-2"] != "no pass" || notes["1-3"] != "open" {
		t.Fatalf("notes: %+v", notes)
	}
	if _, ok := notes["usb1"]; ok {
		t.Fatal("root hub should not be logged")
	}

	m.reloadRules()
	if m.loadErr != nil || len(m.entries) != 0 {
		t.Fatalf("reload empty: %v %+v", m.loadErr, m.entries)
	}
	mustWrite(t, ManagedFile, Render([]Entry{{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "pass"}}))
	m.reloadRules()
	if len(m.entries) != 1 {
		t.Fatalf("reload: %+v", m.entries)
	}
	setGlobal(t, &ManagedFile, root)
	m.reloadRules()
	if m.loadErr == nil {
		t.Fatal("directory rules file")
	}

	setGlobal(t, &sysUSB, root+"/file")
	mustWrite(t, sysUSB, "x")
	m.rescan()
	if !strings.Contains(m.status, "scan:") {
		t.Fatalf("scan error status: %q", m.status)
	}
}

func TestViews(t *testing.T) {
	hidOpen := []Node{{Path: "/dev/hidraw0", Writable: true}, {Path: "/dev/hidraw1", Writable: false}}
	cases := []struct {
		name string
		d    Device
		e    []Entry
		idx  RuleIndex
		want string
	}{
		{"granted", devAt("1-1", "1234", "5678", "Widget", "00", true, hidOpen), []Entry{{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "Acme Widget"}}, nil, "PASS GRANTED"},
		{"vendor", devAt("1-1", "1234", "5678", "Widget", "00", true, nil), []Entry{{Vendor: "1234", Added: "2026-10-04", Label: "Acme"}}, nil, "PASS GRANTED"},
		{"open", devAt("1-1", "1234", "5678", "Widget", "00", true, nil), nil, RuleIndex{{file: "/etc/udev/rules.d/70-other.rules", line: 2, text: `ATTR{idVendor}=="1234", MODE="0660"`}}, "OPEN"},
		{"hid", devAt("1-1", "1234", "5678", "Widget", "00", false, []Node{{Writable: true}}), nil, nil, "HID ONLY"},
		{"pending", devAt("1-1", "1234", "5678", "Widget", "00", false, nil), []Entry{{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "Acme Widget"}}, nil, "PASS PENDING"},
		{"late", devAt("1-1", "1234", "5678", "Widget", "00", false, nil), nil, RuleIndex{{file: "/etc/udev/rules.d/99-late.rules", line: 3, text: `ATTR{idVendor}=="1234", TAG+="uaccess"`}}, "grants nothing"},
		{"none", devAt("1-1", "1234", "5678", "Widget", "00", false, nil), nil, nil, "no udev rule"},
	}
	for _, c := range cases {
		m := baseModel()
		m.all = []Device{c.d}
		m.entries = c.e
		m.idx = c.idx
		m.filter()
		view := m.View()
		if !strings.Contains(view, c.want) {
			t.Errorf("%s: missing %q\n%s", c.name, c.want, stripANSI(view))
		}
		if c.name == "granted" && !strings.Contains(view, "WebHID") {
			t.Errorf("granted card missing WebHID:\n%s", stripANSI(view))
		}
	}
	hub := baseModel()
	hub.showHubs = true
	hub.all = []Device{devAt("1-2", "05e3", "0608", "Hub", "09", true, nil)}
	hub.entries = []Entry{{Vendor: "05e3", Added: "2026-10-04", Label: "Hub"}}
	hub.filter()
	if !strings.Contains(hub.View(), "(hub)") {
		t.Fatal("hub label")
	}

	m := baseModel()
	m.View()
	if got := (model{w: 0, h: 10}).View(); got != "" {
		t.Fatal("zero width")
	}
	m.all = nil
	m.filter()
	if !strings.Contains(m.View(), "no devices") {
		t.Fatal("empty devices")
	}
	// Narrow, no card, inactive seat, sudo not cached, events faded.
	m.w = 160
	m.seat = ""
	m.sudoCached = false
	m.all = []Device{devAt("1-1", "1234", "5678", "Widget", "00", false, nil)}
	m.filter()
	now := time.Now()
	for i := 0; i < 3; i++ {
		m.events = append(m.events, event{at: now, add: i == 0, d: m.all[0], note: "n", nc: cDim})
	}
	if !strings.Contains(m.View(), "no seat") {
		t.Fatal("no seat")
	}
	m.seat = "seat0"
	m.seatActive = false
	m.w = 120
	m.flash = map[string]bool{"1-1": true}
	if !strings.Contains(m.View(), "inactive") {
		t.Fatal("inactive seat")
	}
	m.w = 20
	m.h = 20
	_ = m.viewHeader()
	_ = m.viewFooter()
	m.showHubs = true
	m.tab = 0
	_ = m.viewFooter()

	m = baseModel()
	m.tab = 1
	m.loadErr = errString("boom")
	if !strings.Contains(m.View(), "boom") {
		t.Fatal("load error")
	}
	m.loadErr = nil
	if !strings.Contains(m.View(), "no passes yet") {
		t.Fatal("empty grants")
	}
	m.entries = []Entry{{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "Widget"}}
	m.all = []Device{devAt("1-1", "1234", "5678", "Widget", "00", true, nil)}
	m.cur[1] = 0
	view := m.View()
	if !strings.Contains(view, "WRITTEN TO FILE") || !strings.Contains(view, "1 now") {
		t.Fatalf("rule card:\n%s", stripANSI(view))
	}
	m.entries[0].Product = ""
	m.all = nil
	if !strings.Contains(stripANSI(strings.Join(m.ruleCard(40, 6), "\n")), "nothing connected") && !strings.Contains(stripANSI(strings.Join(m.ruleCard(40, 30), "\n")), "every product") {
		t.Fatal("vendor rule card")
	}
	_ = m.ruleCard(30, 4)
	m.entries = nil
	if !strings.Contains(strings.Join(m.ruleCard(20, 5), ""), "no pass selected") {
		t.Fatal("no selection")
	}
	m.tab = 1
	_ = m.viewFooter()

	m = baseModel()
	m.entries = []Entry{{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "Widget"}}
	m.all = []Device{
		devAt("1-1", "1234", "5678", "Widget", "00", true, nil),
		devAt("1-2", "1234", "5678", "Widget 2", "00", true, nil),
	}
	m.confirm = &job{entry: m.entries[0]}
	if !strings.Contains(m.View(), "revoke pass") || !strings.Contains(stripANSI(strings.Join(m.viewConfirm(), "\n")), "2 connected") {
		t.Fatal("confirm many")
	}
	m.all = m.all[:1]
	if !strings.Contains(strings.Join(m.viewConfirm(), "\n"), "1 connected") {
		t.Fatal("confirm one")
	}
	m.all = nil
	if !strings.Contains(strings.Join(m.viewConfirm(), "\n"), "nothing connected") {
		t.Fatal("confirm none")
	}
	m.confirm = nil
	j := job{allow: true, entry: m.entries[0]}
	m.pw = &j
	m.pwBuf = "secret"
	m.pwErr = "sudo: incorrect password"
	if !strings.Contains(m.View(), "incorrect password") {
		t.Fatal("password modal")
	}
	m.pwErr = ""
	m.pw.allow = false
	_ = m.viewPassword()
	m.pw = nil
	j.allow = true
	m.apply = &applying{job: j, steps: []string{"stage", "install"}, text: []string{"stage file", "install file"}, i: 0}
	if !strings.Contains(m.View(), "issuing pass") {
		t.Fatal("apply modal")
	}
	m.apply.i = 1
	m.spin = 3
	_ = m.viewApply()
	m.apply.i = 2
	m.apply.job.allow = false
	if !strings.Contains(strings.Join(m.viewApply(), "\n"), "revoking") {
		t.Fatal("revoke apply")
	}
	m.apply = nil
	m.help = true
	if !strings.Contains(m.View(), "how passes work") {
		t.Fatal("help")
	}
	m.help = false
	if len(m.viewEvents()) == 0 {
		t.Fatal("events box")
	}
	m.live = true
	_ = m.viewEvents()

	// Pass card with the managed-file rule highlighted, and a tiny height so the footer pins.
	m.idx = RuleIndex{{file: ManagedFile, line: 1, text: `ATTR{idVendor}=="1234", TAG+="uaccess"`}}
	m.all = []Device{devAt("1-1", "1234", "5678", "Widget", "00", true, []Node{{Path: "/dev/hidraw0", Writable: true}})}
	m.entries = []Entry{{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "Widget"}}
	m.filter()
	card := m.passCard(36, 8)
	if len(card) != 8 {
		t.Fatalf("pinned card height %d", len(card))
	}
	_ = strings.Join(card, "\n")
}

func TestKeysAndMouse(t *testing.T) {
	stubExec(t, `exit 1`) // sudo is not ready; granting opens the password prompt
	// `r` rescans sysfs. GitHub's Ubuntu image has /sys/bus/usb and this VM does
	// not, so the fixture has to be the thing Scan reads or the device list
	// changes under the later key assertions.
	sys := isolateHostUSB(t)
	writeSysDevice(t, sys, "1-1", map[string]string{
		"idVendor": "1234\n", "idProduct": "5678\n", "busnum": "1\n", "devnum": "3\n",
		"bDeviceClass": "00\n", "manufacturer": "Acme\n", "product": "Widget\n",
	})
	writeSysDevice(t, sys, "1-2", map[string]string{
		"idVendor": "2222\n", "idProduct": "3333\n", "busnum": "1\n", "devnum": "4\n",
		"bDeviceClass": "00\n", "manufacturer": "Acme\n", "product": "Other\n",
	})
	writeSysDevice(t, sys, "1-3", map[string]string{
		"idVendor": "3333\n", "idProduct": "4444\n", "busnum": "1\n", "devnum": "5\n",
		"bDeviceClass": "09\n", "manufacturer": "Acme\n", "product": "Hub\n",
	})
	m := baseModel()
	m.all = []Device{
		devAt("1-1", "1234", "5678", "Widget", "00", false, nil),
		devAt("1-2", "2222", "3333", "Other", "00", false, nil),
		devAt("1-3", "3333", "4444", "Hub", "09", false, nil),
	}
	m.entries = []Entry{{Vendor: "2222", Product: "3333", Added: "2026-10-04", Label: "Other"}}
	m.filter()

	if cmd := m.Init(); cmd == nil {
		t.Fatal("init")
	}
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = nm.(model)
	nm, cmd := m.Update("ignored")
	if cmd != nil {
		t.Fatal("unknown msg")
	}
	m = nm.(model)

	m, cmd = press(m, runeKey("q"))
	if !isQuit(cmd) {
		t.Fatal("q quits")
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !isQuit(cmd) {
		t.Fatal("esc quits")
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuit(cmd) {
		t.Fatal("ctrl+c quits")
	}

	m.help = true
	m, cmd = press(m, runeKey("q"))
	if m.help || cmd != nil {
		t.Fatal("help swallows quit")
	}
	m, _ = press(m, runeKey("?"))
	if !m.help {
		t.Fatal("help")
	}
	m, _ = press(m, runeKey("?"))
	if m.help {
		t.Fatal("second ? closes")
	}

	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != 1 {
		t.Fatal("tab")
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.tab != 0 {
		t.Fatal("shift tab")
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
	m, _ = press(m, runeKey("1"))
	if m.tab != 0 {
		t.Fatal("key 1")
	}
	m, _ = press(m, runeKey("2"))
	if m.tab != 1 {
		t.Fatal("key 2")
	}
	m.tab = 0
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, runeKey("j"))
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyUp})
	m, _ = press(m, runeKey("k"))
	m, _ = press(m, runeKey("G"))
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnd})
	m, _ = press(m, runeKey("g"))
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyHome})
	if m.cur[0] != 0 {
		t.Fatalf("home %d", m.cur[0])
	}
	m, _ = press(m, runeKey("H"))
	if !m.showHubs || !strings.Contains(m.status, "showing hubs") {
		t.Fatal("show hubs")
	}
	m, _ = press(m, runeKey("H"))
	if m.showHubs || !strings.Contains(m.status, "hidden") {
		t.Fatal("hide hubs")
	}
	m, _ = press(m, runeKey("r"))
	if m.status != "rescanned" {
		t.Fatalf("rescan status %q", m.status)
	}
	// r reloads the rules file and drops entries that were only in memory.
	m.entries = []Entry{{Vendor: "2222", Product: "3333", Added: "2026-10-04", Label: "Other"}}
	m, _ = press(m, runeKey("z"))
	m.tab = 1
	m, _ = press(m, runeKey("a"))
	if m.pw != nil || m.confirm != nil {
		t.Fatal("allow on grants tab")
	}

	m.tab = 0
	m.cur[0] = 1 // 2222 already has a product pass
	m, _ = press(m, runeKey("a"))
	if !strings.Contains(m.status, "already has a pass") {
		t.Fatalf("existing pass: %q", m.status)
	}
	m, _ = press(m, runeKey("v"))
	if m.pw == nil || m.pw.entry.Product != "" {
		t.Fatalf("vendor pass should be offered: %+v", m.pw)
	}
	m.pw = nil
	m.entries = []Entry{{Vendor: "2222", Added: "2026-10-04", Label: "vendor"}}
	m, _ = press(m, runeKey("v"))
	if !strings.Contains(m.status, "already has a pass") {
		t.Fatalf("existing vendor: %q", m.status)
	}

	m.cur[0] = 0
	m.entries = nil
	m, _ = press(m, runeKey("a"))
	if m.pw == nil || !m.pw.allow || m.pw.entry.Product != "5678" {
		t.Fatalf("allow prompt: %+v", m.pw)
	}
	// Password editing.
	m, _ = press(m, runeKey("é"))
	m, _ = press(m, tea.KeyMsg{Type: tea.KeySpace})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.pwBuf != "" {
		t.Fatalf("pw buf %q", m.pwBuf)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.pwErr == "" {
		t.Fatal("empty password")
	}
	m, _ = press(m, runeKey("pw"))
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.pw != nil || m.status != "cancelled" {
		t.Fatal("cancel password")
	}

	m.view = nil
	m, _ = press(m, runeKey("a"))
	if m.pw != nil {
		t.Fatal("allow with nothing selected")
	}
	m.filter()
	m, _ = press(m, runeKey("d"))
	if !strings.Contains(m.status, "no hallpass grant") {
		t.Fatalf("revoke none: %q", m.status)
	}
	m.entries = []Entry{{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "Widget"}}
	m, _ = press(m, runeKey("d"))
	if m.confirm == nil {
		t.Fatal("confirm revoke")
	}
	m, _ = press(m, runeKey("n"))
	if m.confirm != nil || !strings.Contains(m.status, "kept") {
		t.Fatal("keep grant")
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDelete})
	if m.confirm == nil {
		t.Fatal("delete key")
	}
	m, _ = press(m, runeKey("y"))
	if m.confirm != nil || m.pw == nil {
		t.Fatal("confirm y asks for sudo")
	}
	m.pw = nil

	m.tab = 1
	m.cur[1] = 0
	m, _ = press(m, runeKey("x"))
	if m.confirm == nil {
		t.Fatal("revoke on grants")
	}
	m.confirm = nil
	m.cur[1] = 5
	m.clamp()
	m, _ = press(m, runeKey("d"))
	if m.confirm == nil {
		t.Fatal("clamped revoke")
	}
	m.apply = &applying{}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("ctrl+c during apply")
	}
	m, cmd = press(m, runeKey("q"))
	if cmd != nil || m.apply == nil {
		t.Fatal("keys ignored while applying")
	}
	m.apply = nil

	// Mouse: tabs, rows, ignored clicks.
	m.confirm = nil
	m.tab = 0
	m.help = true
	nm, _ = m.Update(tea.MouseMsg{X: 30, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = nm.(model)
	if m.tab != 0 {
		t.Fatal("modal eats clicks")
	}
	m.help = false
	nm, _ = m.Update(tea.MouseMsg{X: 30, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = nm.(model)
	if m.tab != 1 {
		t.Fatalf("click grants tab=%d help=%v confirm=%v pw=%v apply=%v w=%d", m.tab, m.help, m.confirm != nil, m.pw != nil, m.apply != nil, m.w)
	}
	nm, _ = m.Update(tea.MouseMsg{X: 16, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = nm.(model)
	if m.tab != 0 {
		t.Fatal("click devices tab")
	}
	nm, _ = m.Update(tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	nm, _ = m.Update(tea.MouseMsg{X: 10, Y: 4, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	m = nm.(model)
	m.filter()
	nm, _ = m.Update(tea.MouseMsg{X: 10, Y: m.listTop() + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = nm.(model)
	if m.cur[0] != 1 {
		t.Fatalf("click row %d", m.cur[0])
	}
	m.w = 80
	nm, _ = m.Update(tea.MouseMsg{X: 70, Y: m.listTop(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = nm.(model)
	m.w = 120
	nm, _ = m.Update(tea.MouseMsg{X: 110, Y: m.listTop(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = nm.(model)
	if m.cur[0] == 0 && m.showCard() {
		// click landed on the card, selection stays
	}
	m.tab = 1
	m.entries = []Entry{{Vendor: "1234", Product: "5678"}, {Vendor: "2222", Product: "3333"}}
	nm, _ = m.Update(tea.MouseMsg{X: 10, Y: m.listTop() + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = nm.(model)
	if m.cur[1] != 1 {
		t.Fatalf("grants click %d", m.cur[1])
	}

	// Load error blocks a grant.
	m.tab = 0
	m.loadErr = errString("unreadable")
	m.cur[0] = 0
	m.entries = nil
	m.filter()
	m, _ = press(m, runeKey("a"))
	if m.pw != nil || !strings.Contains(m.status, "unreadable") {
		t.Fatalf("load error grant: %q", m.status)
	}
}

func TestUpdateMessages(t *testing.T) {
	stubExec(t, `if [ "$1" = sudo ] && [ "$2" = -n ] && [ "$3" = true ]; then exit 0; fi
exit 0`)
	// finish() rescans before it flashes. The device it should flash has to be
	// what Scan returns, not whatever USB bus the runner happens to have.
	sys := isolateHostUSB(t)
	writeSysDevice(t, sys, "1-1", map[string]string{
		"idVendor": "1234\n", "idProduct": "5678\n", "busnum": "1\n", "devnum": "3\n",
		"bDeviceClass": "00\n", "product": "Widget\n",
	})
	m := baseModel()
	m.ticks = 9
	m.apply = &applying{}
	m.status = "keep"
	nm, cmd := m.Update(tickMsg(time.Now()))
	m = nm.(model)
	if m.status != "keep" || cmd == nil || m.ticks != 10 {
		t.Fatalf("tick during apply: %q %v", m.status, cmd)
	}
	m.apply = nil
	m.ticks = 9
	nm, cmd = m.Update(tickMsg(time.Now()))
	m = nm.(model)
	if cmd == nil || m.ticks != 10 || !m.sudoCached {
		t.Fatalf("tenth tick: ticks %d sudo %v", m.ticks, m.sudoCached)
	}
	nm, cmd = m.Update(spinMsg{})
	if cmd != nil {
		t.Fatal("spin without apply")
	}
	m = nm.(model)
	m.apply = &applying{steps: []string{"install", "reload"}, i: 0}
	nm, cmd = m.Update(spinMsg{})
	m = nm.(model)
	if m.spin != 1 || cmd == nil {
		t.Fatal("spin")
	}
	_ = spin()()
	nm, _ = m.Update(stepMsg{step: "nope"})
	m = nm.(model)
	nm, _ = m.Update(stepMsg{step: "reload"})
	m = nm.(model)
	if m.apply.i != 1 {
		t.Fatalf("step i %d", m.apply.i)
	}
	nm, _ = m.Update(stepMsg{step: "done"})
	m = nm.(model)
	if m.apply.i != len(m.apply.steps) {
		t.Fatal("done step")
	}
	m.apply = nil
	nm, cmd = m.Update(stepMsg{step: "install"})
	if cmd != nil {
		t.Fatal("step without apply")
	}
	m = nm.(model)

	j := job{allow: true, entry: Entry{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "Widget"}}
	nm, _ = m.Update(applyDoneMsg{job: j, err: errString("nope")})
	m = nm.(model)
	if !strings.Contains(m.status, "failed") {
		t.Fatalf("fail status %q", m.status)
	}
	authJob := j
	nm, _ = m.Update(applyDoneMsg{job: authJob, err: errString("sudo: incorrect password"), auth: true})
	m = nm.(model)
	if m.pw == nil || m.pwErr == "" {
		t.Fatal("auth failure reopens password")
	}
	m.pw = nil
	m.entries = []Entry{j.entry}
	m.all = []Device{devAt("1-1", "1234", "5678", "Widget", "00", true, nil)}
	nm, cmd = m.Update(applyDoneMsg{job: j})
	m = nm.(model)
	if !strings.Contains(m.status, "pass issued") || len(m.flash) != 1 || cmd == nil {
		t.Fatalf("issued: %q flash %v", m.status, m.flash)
	}
	n := m.flashN
	nm, _ = m.Update(flashDoneMsg{n: n + 1})
	m = nm.(model)
	if m.flash == nil {
		t.Fatal("stale flash should stick")
	}
	nm, _ = m.Update(flashDoneMsg{n: n})
	m = nm.(model)
	if m.flash != nil {
		t.Fatal("flash cleared")
	}
	rev := job{entry: j.entry}
	nm, _ = m.Update(applyDoneMsg{job: rev})
	m = nm.(model)
	if !strings.Contains(m.status, "revoked") {
		t.Fatalf("revoked status %q", m.status)
	}

	lines := readStepsTo(t, "noise\n@@ install\n@@ done\n")
	if len(lines) != 2 || lines[0] != "install" || lines[1] != "done" {
		t.Fatalf("steps %q", lines)
	}
}

func TestRunApplyGoroutine(t *testing.T) {
	isolateHostUSB(t)
	stubExec(t, `if [ "$1" = sudo ] && [ "$2" = -n ] && [ "$3" = true ]; then exit 0; fi
if [ -n "$CAPTURE_STDIN" ]; then cat > "$CAPTURE_STDIN"; fi
printf '%s\n' "@@ install" "@@ reload" "@@ done"
exit 0`)
	m := baseModel()
	bad := job{allow: true, entry: Entry{Vendor: "nope"}}
	nm, cmd := m.run(bad, "")
	m = nm.(model)
	if cmd != nil || !strings.Contains(m.status, "invalid") {
		t.Fatalf("invalid run: %v %q", cmd, m.status)
	}

	dir := t.TempDir()
	setGlobal(t, &ManagedFile, dir+"/70-local-usb.rules")
	okJob := job{allow: true, entry: Entry{Vendor: "1234", Product: "5678", Added: "2026-10-04", Label: "Widget"}}
	stdin := dir + "/stdin"
	t.Setenv("CAPTURE_STDIN", stdin)
	nm, _ = m.run(okJob, "s3cret")
	m = nm.(model)
	m = waitApplyDone(t, m)
	if m.apply != nil || !strings.Contains(m.status, "pass issued") {
		t.Fatalf("run ok: apply %v status %q", m.apply, m.status)
	}
	gotb, err := os.ReadFile(stdin)
	got := string(gotb)
	if err != nil || !strings.Contains(got, "s3cret") {
		t.Fatalf("stdin %q %v", got, err)
	}

	stubExec(t, `if [ "$1" = sudo ] && [ "$2" = -n ] && [ "$3" = true ]; then exit 0; fi
echo "noise" >&2
echo "sudo: try again" >&2
exit 1`)
	if err := os.WriteFile(ManagedFile, []byte(Render([]Entry{okJob.entry})), 0o644); err != nil {
		t.Fatal(err)
	}
	nm, _ = m.run(job{entry: okJob.entry}, "")
	m = nm.(model)
	m = waitApplyDone(t, m)
	if m.pw == nil || !strings.Contains(m.pwErr, "incorrect") {
		t.Fatalf("auth: %+v %q", m.pw, m.pwErr)
	}

	stubExec(t, `if [ "$1" = sudo ] && [ "$2" = -n ] && [ "$3" = true ]; then exit 0; fi
echo "line one" >&2
echo "could not remove acl" >&2
exit 1`)
	m.pw = nil
	nm, _ = m.run(okJob, "")
	m = nm.(model)
	m = waitApplyDone(t, m)
	if !strings.Contains(m.status, "could not remove acl") {
		t.Fatalf("fail status %q", m.status)
	}

	stubExec(t, `if [ "$1" = sudo ] && [ "$2" = -n ] && [ "$3" = true ]; then exit 0; fi
exit 1`)
	nm, _ = m.run(okJob, "")
	m = nm.(model)
	m = waitApplyDone(t, m)
	if !strings.Contains(m.status, "exit status") {
		t.Fatalf("empty stderr: %q", m.status)
	}

	prev := command
	command = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/no/such/hallpass-stub")
	}
	t.Cleanup(func() { command = prev })
	nm, _ = m.run(okJob, "")
	m = nm.(model)
	m = waitApplyDone(t, m)
	if !strings.Contains(m.status, "failed") {
		t.Fatalf("start failure: %q", m.status)
	}
}

func waitApplyDone(t *testing.T, m model) model {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if m.apply == nil {
			t.Fatal("apply cleared before a done message")
		}
		select {
		case msg := <-m.apply.ch:
			nm, _ := m.Update(msg)
			m = nm.(model)
			if _, ok := msg.(applyDoneMsg); ok {
				return m
			}
		case <-time.After(time.Until(deadline)):
		}
	}
	t.Fatal("timed out")
	return m
}

func readStepsTo(t *testing.T, text string) []string {
	t.Helper()
	ch := make(chan tea.Msg, 8)
	readSteps(strings.NewReader(text), ch)
	close(ch)
	var out []string
	for msg := range ch {
		out = append(out, msg.(stepMsg).step)
	}
	return out
}

func TestNewModelSmoke(t *testing.T) {
	m := newModel()
	m.w, m.h = 120, 36
	if !strings.Contains(m.View(), "hallpass") {
		t.Fatal("new model view")
	}
	if m.user == "" {
		t.Fatal("user")
	}
}

// isolateHostUSB points Scan and the rules file at a temp tree. Tests that
// rescan or reload must not read the machine's /sys/bus/usb or /etc/udev.
func isolateHostUSB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	sys := filepath.Join(root, "sys")
	rules := filepath.Join(root, "rules")
	if err := os.MkdirAll(sys, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	setGlobal(t, &sysUSB, sys)
	setGlobal(t, &devBusDir, filepath.Join(root, "bus"))
	setGlobal(t, &devDir, filepath.Join(root, "dev"))
	setGlobal(t, &ManagedFile, filepath.Join(root, "70-local-usb.rules"))
	setGlobal(t, &ruleDirs, []string{rules})
	return sys
}

func writeSysDevice(t *testing.T, sys, name string, attrs map[string]string) {
	t.Helper()
	for k, v := range attrs {
		mustWrite(t, filepath.Join(sys, name, k), v)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
