package main

import (
	"os"
	"os/exec"
	"os/user"
	"strings"
)

// applyScript installs (or removes) the managed file, reloads udev, and
// replays "change" events so the ACL flips without a re-plug. udev never
// removes an ACL it added, so revoked nodes are stripped first; the change
// event then re-adds it if some other rule still grants access.
// $1 = staged file ("" to remove), $2 = target, $3 = user,
// $4 = space-separated nodes to strip, $5.. = vendor ids to retrigger.
const applyScript = `set -e
if [ -n "$1" ]; then install -m 0644 "$1" "$2"; else rm -f "$2"; fi
udevadm control --reload
for n in $4; do setfacl -x "u:$3" "$n" 2>/dev/null || true; done
shift 4
for v in "$@"; do
  udevadm trigger --action=change --subsystem-match=usb --attr-match=idVendor="$v"
done
udevadm trigger --action=change --subsystem-match=hidraw
udevadm settle --timeout=5`

// ApplyCmd stages the new managed file and returns the privileged command
// that installs it. The caller runs it (directly, or with the terminal handed
// over when sudo needs a password) and then calls cleanup.
func ApplyCmd(entries []Entry, vendors, strip []string) (cmd *exec.Cmd, cleanup func(), err error) {
	staged := ""
	cleanup = func() {}
	if len(entries) > 0 {
		f, err := os.CreateTemp("", "hallpass-*.rules")
		if err != nil {
			return nil, nil, err
		}
		if _, err := f.WriteString(Render(entries)); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, nil, err
		}
		f.Close()
		staged = f.Name()
		cleanup = func() { os.Remove(staged) }
	}
	u, err := user.Current()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	args := append([]string{"sh", "-c", applyScript, "hallpass", staged, ManagedFile, u.Username, strings.Join(strip, " ")}, vendors...)
	return exec.Command("sudo", args...), cleanup, nil
}

// NodesFor lists the device nodes of connected devices an entry covers, so a
// revoke can strip their ACLs.
func NodesFor(e Entry, devs []Device) []string {
	var out []string
	for _, d := range devs {
		if !e.Covers(d) {
			continue
		}
		out = append(out, d.Devnode)
		for _, n := range d.Hidraw {
			out = append(out, n.Path)
		}
	}
	return out
}

// SudoReady reports whether sudo will run without prompting.
func SudoReady() bool {
	return exec.Command("sudo", "-n", "true").Run() == nil
}
