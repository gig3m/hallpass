package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
)

// applyScript installs (or removes) the managed file, reloads udev, and
// replays "change" events so the ACL flips without a re-plug.
//
// It holds a lock and refuses to write if the file no longer has the hash it
// had when hallpass read it, so two hallpass instances can't silently undo
// each other. On revoke, udev never removes an ACL it added, so the script
// strips it from every node of every currently connected matching device,
// found at apply time (not when the user pressed d), after the new rules are
// loaded; the change event then re-adds it where another rule still grants.
// A failure to strip a node that still exists fails the whole apply.
//
// Each step prints an "@@ <step>" marker so the TUI can show progress.
// $1 = staged file ("" to remove), $2 = target, $3 = user,
// $4 = "vid:pid" or "vid:" to strip ("" for none), $5 = expected sha256 of
// the target ("" = must not exist), $6.. = vendor ids to retrigger.
const applyScript = `set -eu
exec 9>/run/hallpass.lock
flock 9
target=$2 user=$3 strip=$4 want=$5
have=$(sha256sum "$target" 2>/dev/null | cut -d' ' -f1 || true)
if [ "$have" != "$want" ]; then
  echo "hallpass: $target changed since hallpass read it; reload and try again" >&2
  exit 3
fi
if [ -n "$strip" ] && ! command -v setfacl >/dev/null; then
  echo "hallpass: setfacl not found (install the acl package)" >&2
  exit 4
fi
echo "@@ install"
if [ -n "$1" ]; then install -m 0644 "$1" "$target"; else rm -f "$target"; fi
echo "@@ reload"
udevadm control --reload
if [ -n "$strip" ]; then
  echo "@@ strip"
  vid=${strip%%:*} pid=${strip#*:}
  for d in /sys/bus/usb/devices/*; do
    v=$(cat "$d/idVendor" 2>/dev/null) || continue
    [ "$v" = "$vid" ] || continue
    if [ -n "$pid" ]; then
      p=$(cat "$d/idProduct" 2>/dev/null) || continue
      [ "$p" = "$pid" ] || continue
    fi
    bus=$(cat "$d/busnum" 2>/dev/null) || continue
    dev=$(cat "$d/devnum" 2>/dev/null) || continue
    nodes=$(printf '/dev/bus/usb/%03d/%03d' "$bus" "$dev")
    for h in "$d"/*:*/*/hidraw/hidraw*; do
      if [ -e "$h" ]; then nodes="$nodes /dev/${h##*/}"; fi
    done
    for n in $nodes; do
      [ -e "$n" ] || continue
      if ! setfacl -x "u:$user" "$n"; then
        if [ -e "$n" ]; then
          echo "hallpass: could not remove $user's ACL from $n" >&2
          exit 5
        fi
      fi
    done
  done
fi
shift 5
echo "@@ trigger"
for v in "$@"; do
  udevadm trigger --action=change --subsystem-match=usb --attr-match=idVendor="$v"
done
udevadm trigger --action=change --subsystem-match=hidraw
echo "@@ settle"
udevadm settle --timeout=5
echo "@@ done"`

// exitConflict is the script's exit status when the file changed underneath.
const exitConflict = 3

// ApplyCmd reads the managed file now, applies the change to it, stages the
// result, and returns the privileged command that installs it. sudoFlags
// picks how sudo authenticates (nil: prompt on the terminal, "-n": cached
// only, "-S": password on stdin). The caller runs it and then calls cleanup.
func ApplyCmd(c Change, sudoFlags []string) (cmd *exec.Cmd, cleanup func(), err error) {
	current, hash, err := loadEntriesFrom(ManagedFile)
	if err != nil {
		return nil, nil, err
	}
	entries, err := c.apply(current)
	if err != nil {
		return nil, nil, err
	}
	vendors := map[string]bool{}
	for _, e := range entries {
		if !e.Valid() {
			return nil, nil, fmt.Errorf("refusing to write invalid entry %q", e.Match())
		}
	}
	strip := ""
	switch {
	case c.Add != nil:
		if !c.Add.Valid() {
			return nil, nil, fmt.Errorf("refusing to write invalid entry %q", c.Add.Match())
		}
		vendors[c.Add.Vendor] = true
	case c.Remove != "":
		vid, pid, _ := strings.Cut(c.Remove, ":")
		if pid == "*" {
			pid = ""
		}
		strip = vid + ":" + pid
		vendors[vid] = true
	default: // rewrite: retrigger everything the file covers
		for _, e := range entries {
			vendors[e.Vendor] = true
		}
	}

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
	args := append(append([]string{}, sudoFlags...), "sh", "-c", applyScript, "hallpass", staged, ManagedFile, currentUser(), strip, hash)
	for v := range vendors {
		args = append(args, v)
	}
	return exec.Command("sudo", args...), cleanup, nil
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
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
