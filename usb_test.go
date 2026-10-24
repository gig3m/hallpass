package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hallpass-ids")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(dir, "usb.ids")
	fixture := "" +
		"# comment\n" +
		"\n" +
		"short\n" +
		"1234  Test Vendor\n" +
		"\t5678  Test Product\n" +
		"\tAbCd  Mixed Case\n" +
		"\t\tnot a product\n" +
		"abcd  Other Vendor\n" +
		"C 00  (Device)\n" +
		"ffff  After Class\n"
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		panic(err)
	}
	usbIDPaths = []string{filepath.Join(dir, "missing.ids"), path}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestDevicePredicates(t *testing.T) {
	d := Device{SysName: "1-1", Vendor: "1234", Product: "5678", Dev: 3}
	if d.ID() != "1234:5678" || d.Key() != "1-1#3" {
		t.Fatalf("id/key: %s %s", d.ID(), d.Key())
	}
	if d.IsHub() || d.IsRootHub() {
		t.Fatal("plain device")
	}
	hub := Device{SysName: "usb1", Class: "09"}
	if !hub.IsHub() || !hub.IsRootHub() {
		t.Fatal("root hub")
	}
	if (Device{SysName: "1-1"}).IsRootHub() {
		t.Fatal("not a root hub")
	}

	open := Node{Writable: true}
	locked := Node{Writable: false}
	if (Device{Writable: true, Hidraw: []Node{open}}).HidOnly() {
		t.Fatal("writable device is not hid-only")
	}
	if (Device{Hidraw: nil}).HidOnly() {
		t.Fatal("no hidraw")
	}
	if (Device{Hidraw: []Node{open, locked}}).HidOnly() {
		t.Fatal("one hidraw locked")
	}
	if !(Device{Hidraw: []Node{open, open}}).HidOnly() {
		t.Fatal("all hidraw open")
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		d    Device
		want string
	}{
		{Device{Vendor: "0000", Product: "0000"}, "(unknown device)"},
		{Device{Vendor: "1234", Product: "0000"}, "Test Vendor"},
		{Device{Vendor: "1234", Product: "5678"}, "Test Product"},
		{Device{Vendor: "0000", Name: "Widget"}, "Widget"},
		{Device{Manufacturer: "Acme", Name: "Acme Widget"}, "Acme Widget"},
		{Device{Manufacturer: "Acme Corp", Name: "Widget"}, "Acme Corp Widget"},
		{Device{Vendor: "1234", Product: "abcd"}, "Test Vendor Mixed Case"},
	}
	for _, c := range cases {
		if got := c.d.Label(); got != c.want {
			t.Errorf("Label(%+v)=%q want %q", c.d, got, c.want)
		}
	}
	if got := usbIDs().vendor("ffff"); got != "" {
		t.Fatalf("class section should stop the parse, got %q", got)
	}
	if firstWord("") != "" || firstWord("  ") != "  " {
		t.Fatalf("firstWord edges: %q %q", firstWord(""), firstWord("  "))
	}
}

func TestLookupAndACL(t *testing.T) {
	if userName(0) != "root" {
		t.Fatalf("root user: %q", userName(0))
	}
	if userName(0) != "root" {
		t.Fatal("cache miss")
	}
	if groupName(0) == "" {
		t.Fatal("group 0")
	}
	missing := lookupID(filepath.Join(t.TempDir(), "nope"), 7, map[uint32]string{})
	if missing != "7" {
		t.Fatalf("missing file: %q", missing)
	}
	file := filepath.Join(t.TempDir(), "passwd")
	os.WriteFile(file, []byte("ada:x:42:42:::\n"), 0o644)
	cache := map[uint32]string{}
	if got := lookupID(file, 42, cache); got != "ada" {
		t.Fatalf("found: %q", got)
	}
	if got := lookupID(file, 42, cache); got != "ada" {
		t.Fatal("cached")
	}
	if got := lookupID(file, 99, map[uint32]string{}); got != "99" {
		t.Fatalf("unknown id: %q", got)
	}

	path := filepath.Join(t.TempDir(), "node")
	os.WriteFile(path, nil, 0o644)
	if ACLUsers(path) != nil {
		t.Fatal("no xattr should be nil")
	}
	if ACLUsers(filepath.Join(t.TempDir(), "missing")) != nil {
		t.Fatal("missing path")
	}
	buf := make([]byte, 4+8+8+3)
	buf[4] = 0x02 // ACL_USER, uid 0
	buf[12] = 0x08
	got := aclUsers(buf)
	if len(got) != 1 || got[0] != "root" {
		t.Fatalf("acl users: %q", got)
	}
	if aclUsers(make([]byte, 4)) != nil {
		t.Fatal("header only")
	}
}

func TestScanFixture(t *testing.T) {
	root := t.TempDir()
	sys := filepath.Join(root, "sys")
	bus := filepath.Join(root, "bus")
	dev := filepath.Join(root, "dev")
	setGlobal(t, &sysUSB, sys)
	setGlobal(t, &devBusDir, bus)
	setGlobal(t, &devDir, dev)

	writeAttr := func(dir, name, body string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	node := func(rel string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, mode); err != nil {
			t.Fatal(err)
		}
	}

	hub := filepath.Join(sys, "usb1")
	writeAttr(hub, "idVendor", "1d6b\n")
	writeAttr(hub, "idProduct", "0002\n")
	writeAttr(hub, "busnum", "1\n")
	writeAttr(hub, "devnum", "1\n")
	writeAttr(hub, "bDeviceClass", "09\n")
	writeAttr(hub, "product", "Linux Hub\n")
	node("bus/001/001", 0o644)

	board := filepath.Join(sys, "1-1")
	writeAttr(board, "idVendor", "1234\n")
	writeAttr(board, "idProduct", "5678\n")
	writeAttr(board, "busnum", "1\n")
	writeAttr(board, "devnum", "3\n")
	writeAttr(board, "bDeviceClass", "00\n")
	writeAttr(board, "manufacturer", "Acme\nCorp\n")
	writeAttr(board, "product", "Widget\n")
	writeAttr(board, "serial", "s\n1\n")
	hid := filepath.Join(board, "1-1:1.0", "0003:1234:5678.0001", "hidraw")
	writeAttr(filepath.Join(hid, "hidraw2"), "dev", "x")
	writeAttr(filepath.Join(hid, "hidraw0"), "dev", "x")
	node("dev/hidraw0", 0o644)
	node("dev/hidraw2", 0o444)
	node("bus/001/003", 0o644)

	hidOnly := filepath.Join(sys, "2-1")
	writeAttr(hidOnly, "idVendor", "0eee\n")
	writeAttr(hidOnly, "idProduct", "0001\n")
	writeAttr(hidOnly, "busnum", "2\n")
	writeAttr(hidOnly, "devnum", "1\n")
	writeAttr(hidOnly, "bDeviceClass", "00\n")
	writeAttr(filepath.Join(hidOnly, "2-1:1.0", "0003:0EEE:0001.0001", "hidraw", "hidraw9"), "dev", "x")
	node("dev/hidraw9", 0o644)
	node("bus/002/001", 0o444)

	writeAttr(filepath.Join(sys, "2-2"), "busnum", "2\n") // no idVendor
	if err := os.MkdirAll(filepath.Join(sys, "1-1:1.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	devs, err := Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 3 {
		t.Fatalf("want 3 devices, got %+v", devs)
	}
	if devs[0].SysName != "1-1" || devs[1].SysName != "usb1" || devs[2].SysName != "2-1" {
		t.Fatalf("order: %s %s %s", devs[0].SysName, devs[1].SysName, devs[2].SysName)
	}
	boardDev := devs[0]
	if !boardDev.Writable || boardDev.Label() != "Acme Corp Widget" || boardDev.Serial != "s 1" || boardDev.Manufacturer != "Acme Corp" {
		t.Fatalf("board: %+v", boardDev)
	}
	if len(boardDev.Hidraw) != 2 || boardDev.Hidraw[0].Path != filepath.Join(dev, "hidraw0") || !boardDev.Hidraw[0].Writable || boardDev.Hidraw[1].Writable {
		t.Fatalf("hidraw: %+v", boardDev.Hidraw)
	}
	if !strings.Contains(boardDev.Owner, "0644") {
		t.Fatalf("owner: %q", boardDev.Owner)
	}
	if !devs[1].IsRootHub() || !devs[1].IsHub() {
		t.Fatalf("hub: %+v", devs[1])
	}
	if devs[2].Writable || !devs[2].HidOnly() || devs[2].Owner == "" {
		t.Fatalf("hid-only: %+v owner %q", devs[2], devs[2].Owner)
	}

	if readAttr(board, "nope") != "" {
		t.Fatal("missing attr")
	}
	if !canOpen(filepath.Join(bus, "001", "003")) || canOpen(filepath.Join(bus, "002", "001")) || canOpen(filepath.Join(bus, "missing")) {
		t.Fatal("canOpen")
	}
	if ownerOf(filepath.Join(bus, "missing")) != "missing" {
		t.Fatal("owner missing")
	}
	if nodes := hidrawNodes(filepath.Join(sys, "usb1")); len(nodes) != 0 {
		t.Fatalf("hub hidraw: %+v", nodes)
	}

	setGlobal(t, &sysUSB, filepath.Join(root, "not-a-dir"))
	if err := os.WriteFile(sysUSB, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(); err == nil {
		t.Fatal("scan of a file should fail")
	}
}
