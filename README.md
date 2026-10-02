# hallpass

Grant yourself access to USB devices on Linux without running things as root.

Browsers (WebUSB, WebHID) and userspace flashers (dfu-util, Pybricks, vendor configurators) open USB device nodes as you, but those nodes are `root:root 0664` unless a udev rule says otherwise. hallpass is a TUI for writing those rules: pick a device, press `a`, and the access applies right away without re-plugging.

```
hallpass                      TUI
hallpass list                 connected devices and whether you can open them
hallpass rules                current grants
hallpass allow VID[:PID]      grant a device, or a whole vendor without :PID
hallpass revoke VID[:PID|*]   remove a grant
```

## How it works

- Grants live in one file, `/etc/udev/rules.d/70-local-usb.rules`, and each one tags the device's raw USB node and its hidraw nodes with `uaccess`. logind then gives the user at the active seat an ACL on those nodes; nobody joins a group.
- The file is numbered `70-` on purpose. `uaccess` only works in a file that sorts before `73-seat-late.rules`. A `99-*.rules` uaccess rule still tags the device but grants nothing, and udev doesn't warn you. hallpass flags rules like that with ⚠.
- After changing the file it reloads udev and retriggers matching devices. On revoke it also strips the ACL from connected devices before retriggering, because udev never removes an ACL it added. If another rule still grants access, the retrigger adds it back.
- Vendor grants (`v`) are for devices that change product ID between modes, like a LEGO hub (`0694:0009` normally, `0694:0008` in DFU mode). The usb_device line matches with `ATTR`, not `ATTRS`, so a vendor grant for a hub doesn't spread to everything plugged into it.
- It runs as you and calls `sudo` only for the write and reload. If sudo needs a password, the TUI asks for it in its own prompt and hands it to `sudo -S`; the apply dialog then shows each step as the privileged script reports it.

In the TUI, each device gets a pass card showing its nodes, what will work (WebUSB, libusb/dfu-util, WebHID), and every udev rule that mentions it. The Grants tab shows the exact lines written to the rules file. `?` opens help, `1`/`2` or tab switch tabs, and rows are clickable.

Status marks: ✓ you can open the device, ◐ only its hidraw nodes are open (WebHID works, WebUSB doesn't), ✗ root only.

## Install

```
go install github.com/gig3m/hallpass@latest
```

or from a checkout, `go build -o ~/.local/bin/hallpass .`

Linux only, with systemd-logind (that's what turns `uaccess` into ACLs). Needs `udevadm`, `setfacl` (acl), and sudo.

## License

MIT
