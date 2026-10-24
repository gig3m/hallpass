# Contributing

hallpass is a small Linux program. The changes that help are the ones that keep a pass narrow and a rules file honest.

## Bugs and ideas

Use a [bug report](https://github.com/gig3m/hallpass/issues/new?template=bug_report.yml) or a [feature request](https://github.com/gig3m/hallpass/issues/new?template=feature_request.yml).

For a bug, include `hallpass --version`, the distro and session (X11 or Wayland), and the output of `hallpass list` and `hallpass rules`. A browser error from the page that failed to open the device helps. Leave out sudo passwords and serial numbers you don't want public.

## Building and testing

The Go version is the one in `go.mod`.

```sh
go test ./...
gofmt -l .
go vet ./...
```

CI runs those, and it fails if statement coverage drops below 80%. `gofmt -l .` must print nothing.

`TestApplyScriptConflict` really runs the privileged script: it uses `sudo` and reloads udev. It stays skipped unless you set `HALLPASS_LIVE=1`, and CI does not set that.

## Pull requests

Say what a person can do after the change, or what was broken. Add a test for anything that decides whether a device is open, how a rule is rendered, or how an entry is parsed. The TUI can be driven by calling `Update` with `tea.Msg` values; `tui_test.go` does that.

Review a change to the generated rule as if it will run as root on every plug. A pass should keep granting `uaccess` to the active seat. It should keep matching the device itself (`ATTR`, and `ID_VENDOR_ID` / `ID_MODEL_ID` on hidraw), so a hub pass does not open devices plugged into the hub.

## License

This project is [MIT](LICENSE). By submitting a patch you agree it can be distributed under that license.
