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
// Each step prints an "@@ <step>" marker so the TUI can show progress.
// $1 = staged file ("" to remove), $2 = target, $3 = user,
// $4 = space-separated nodes to strip, $5.. = vendor ids to retrigger.
const applyScript = `set -e
echo "@@ install"
if [ -n "$1" ]; then install -m 0644 "$1" "$2"; else rm -f "$2"; fi
echo "@@ reload"
udevadm control --reload
if [ -n "$4" ]; then
  echo "@@ strip"
  for n in $4; do setfacl -x "u:$3" "$n" 2>/dev/null || true; done
fi
shift 4
echo "@@ trigger"
for v in "$@"; do
  udevadm trigger --action=change --subsystem-match=usb --attr-match=idVendor="$v"
done
udevadm trigger --action=change --subsystem-match=hidraw
echo "@@ settle"
udevadm settle --timeout=5
echo "@@ done"`

// ApplyCmd stages the new managed file and returns the privileged command
// that installs it. sudoFlags picks how sudo authenticates (nil: prompt on
// the terminal, "-n": cached only, "-S": password on stdin). The caller runs
// it and then calls cleanup.
func ApplyCmd(entries []Entry, vendors, strip, sudoFlags []string) (cmd *exec.Cmd, cleanup func(), err error) {
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
	args := append(append([]string{}, sudoFlags...), "sh", "-c", applyScript, "hallpass", staged, ManagedFile, currentUser(), strings.Join(strip, " "))
	return exec.Command("sudo", append(args, vendors...)...), cleanup, nil
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
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

// Seat describes the login seat, since uaccess only grants to the user at
// the active seat; over plain SSH there is none.
func Seat() (seat string, active bool) {
	seat = os.Getenv("XDG_SEAT")
	id := os.Getenv("XDG_SESSION_ID")
	if seat == "" || id == "" {
		return seat, false
	}
	out, err := exec.Command("loginctl", "show-session", id, "-p", "Active", "--value").Output()
	return seat, err == nil && strings.TrimSpace(string(out)) == "yes"
}
