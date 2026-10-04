// hallpass grants the logged-in user access to USB devices via udev uaccess
// rules, so WebUSB/WebHID and userspace flashers work without root.
package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const usage = `hallpass: grant yourself access to USB devices

usage:
  hallpass                      interactive TUI
  hallpass list                 connected devices and whether you can open them
  hallpass rules                grants in ` + ManagedFile + `
  hallpass allow VID[:PID]      grant a device (or a whole vendor without :PID)
  hallpass revoke VID[:PID|*]   remove a grant
  hallpass rewrite              regenerate the rules file (after upgrading hallpass)
  hallpass --version
`

// version is set at release build time with -ldflags "-X main.version=...".
var version = ""

// buildVersion falls back to the module version, so go install @vX works too.
func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "dev"
}

func main() {
	if os.Geteuid() == 0 {
		fail("don't run hallpass with sudo or as root; run plain `hallpass` as your normal user.\n" +
			"It calls sudo itself when it writes the rules file, and as root every device looks accessible.")
	}
	args := os.Args[1:]
	if len(args) == 0 {
		final, err := tea.NewProgram(newModel(), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
		if err != nil {
			fail(err.Error())
		}
		if m, ok := final.(model); ok {
			fmt.Println(m.quitLine())
		}
		return
	}
	var err error
	switch args[0] {
	case "list", "ls":
		err = cmdList()
	case "rules":
		err = cmdRules()
	case "allow":
		err = needArg(args, cmdAllow)
	case "revoke", "rm":
		err = needArg(args, cmdRevoke)
	case "rewrite":
		err = cmdRewrite()
	case "-v", "--version", "version":
		fmt.Println("hallpass", buildVersion())
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "hallpass:", msg)
	os.Exit(1)
}

func needArg(args []string, f func(string) error) error {
	if len(args) != 2 {
		return fmt.Errorf("%s needs one VID[:PID] argument", args[0])
	}
	return f(strings.ToLower(args[1]))
}

func cmdList() error {
	devs, err := Scan()
	if err != nil {
		return err
	}
	idx := LoadRuleIndex()
	for _, d := range devs {
		if d.IsRootHub() {
			continue
		}
		mark := "✗"
		if d.Writable {
			mark = "✓"
		} else if d.HidOnly() {
			mark = "◐"
		}
		fmt.Printf("%s %s  %-8s %-14s %s\n", mark, d.ID(), d.SysName, d.Owner, d.Label())
		for _, r := range idx.For(d) {
			warn := ""
			if r.Late() {
				warn = "  ⚠ sorts after " + seatLate + ", grants nothing"
			}
			fmt.Printf("      %s%s\n", r, warn)
		}
	}
	return nil
}

func cmdRules() error {
	entries, err := LoadEntries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("no grants in", ManagedFile)
	}
	for _, e := range entries {
		fmt.Printf("%-10s %s  %s\n", e.Match(), e.Added, e.Label)
	}
	return nil
}

var idArg = regexp.MustCompile(`^([0-9a-f]{4})(?::([0-9a-f]{4}|\*))?$`)

func parseID(s string) (vid, pid string, err error) {
	m := idArg.FindStringSubmatch(s)
	if m == nil {
		return "", "", fmt.Errorf("bad id %q, want VID or VID:PID in hex (e.g. 0694:0008)", s)
	}
	if m[2] == "*" {
		m[2] = ""
	}
	return m[1], m[2], nil
}

func cmdAllow(arg string) error {
	vid, pid, err := parseID(arg)
	if err != nil {
		return err
	}
	// Prefer the connected device's own strings for the label.
	d := Device{Vendor: vid, Product: pid}
	if devs, err := Scan(); err == nil {
		for _, x := range devs {
			if x.Vendor == vid && (pid == "" || x.Product == pid) {
				d = x
				d.Product = pid
				break
			}
		}
	}
	e := NewEntry(d, pid == "")
	if pid != "" && d.Name == "" {
		e.Label = cleanText(d.Label(), 80)
	}
	if err := runApply(Change{Add: &e}); err != nil {
		return err
	}
	fmt.Printf("allowed %s (%s)\n", e.Match(), e.Label)
	return nil
}

func cmdRevoke(arg string) error {
	vid, pid, err := parseID(arg)
	if err != nil {
		return err
	}
	match := vid + ":*"
	if pid != "" {
		match = vid + ":" + pid
	}
	if err := runApply(Change{Remove: match}); err != nil {
		return err
	}
	fmt.Printf("revoked %s\n", match)
	return nil
}

// cmdRewrite regenerates the managed file from its entries, e.g. after an
// upgrade that changes the rule format.
func cmdRewrite() error {
	entries, err := LoadEntries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("no grants in", ManagedFile)
		return nil
	}
	if err := runApply(Change{}); err != nil {
		return err
	}
	fmt.Printf("rewrote %d grant%s in %s\n", len(entries), plural(len(entries)), ManagedFile)
	return nil
}

func runApply(c Change) error {
	cmd, cleanup, err := ApplyCmd(c, nil)
	if err != nil {
		return err
	}
	defer cleanup()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, io.Discard, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("applying rules: %w", err)
	}
	time.Sleep(200 * time.Millisecond) // logind applies the ACL just after settle
	return nil
}
