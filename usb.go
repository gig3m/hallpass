package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const sysUSB = "/sys/bus/usb/devices"

// Device is one USB device (not an interface) as seen in sysfs.
type Device struct {
	SysName      string // e.g. 5-1.3
	Vendor       string // idVendor, lowercase hex
	Product      string // idProduct, lowercase hex
	Manufacturer string
	Name         string
	Serial       string
	Bus, Dev     int
	Class        string // bDeviceClass
	Devnode      string
	Writable     bool
	Owner        string // "user:group mode" of the devnode
	Hidraw       []Node
}

// Node is a secondary device node hanging off a USB device (hidraw for WebHID).
type Node struct {
	Path     string
	Writable bool
}

func (d Device) ID() string { return d.Vendor + ":" + d.Product }

// Key identifies one enumeration of a device; a re-plug gets a new devnum.
func (d Device) Key() string { return fmt.Sprintf("%s#%d", d.SysName, d.Dev) }

// HidOnly: the raw USB node is locked but every hidraw node is open.
func (d Device) HidOnly() bool {
	if d.Writable || len(d.Hidraw) == 0 {
		return false
	}
	for _, n := range d.Hidraw {
		if !n.Writable {
			return false
		}
	}
	return true
}

func (d Device) IsHub() bool { return d.Class == "09" }

func (d Device) IsRootHub() bool { return strings.HasPrefix(d.SysName, "usb") }

// Label is the most human name available: product string, else usb.ids.
func (d Device) Label() string {
	name := strings.TrimSpace(d.Name)
	if name == "" {
		name = usbIDs().product(d.Vendor, d.Product)
	}
	vendor := strings.TrimSpace(d.Manufacturer)
	if vendor == "" {
		vendor = usbIDs().vendor(d.Vendor)
	}
	switch {
	case name == "" && vendor == "":
		return "(unknown device)"
	case name == "":
		return vendor
	case vendor == "" || strings.Contains(strings.ToLower(name), strings.ToLower(firstWord(vendor))):
		return name
	default:
		return vendor + " " + name
	}
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}

func readAttr(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Scan enumerates USB devices from sysfs, sorted by bus topology.
func Scan() ([]Device, error) {
	entries, err := os.ReadDir(sysUSB)
	if err != nil {
		return nil, err
	}
	var devs []Device
	for _, e := range entries {
		name := e.Name()
		if strings.Contains(name, ":") { // interface, not a device
			continue
		}
		dir := filepath.Join(sysUSB, name)
		vid := readAttr(dir, "idVendor")
		if vid == "" {
			continue
		}
		bus, _ := strconv.Atoi(readAttr(dir, "busnum"))
		dev, _ := strconv.Atoi(readAttr(dir, "devnum"))
		d := Device{
			SysName:      name,
			Vendor:       strings.ToLower(vid),
			Product:      strings.ToLower(readAttr(dir, "idProduct")),
			Manufacturer: readAttr(dir, "manufacturer"),
			Name:         readAttr(dir, "product"),
			Serial:       readAttr(dir, "serial"),
			Bus:          bus,
			Dev:          dev,
			Class:        readAttr(dir, "bDeviceClass"),
			Devnode:      fmt.Sprintf("/dev/bus/usb/%03d/%03d", bus, dev),
		}
		d.Writable = canOpen(d.Devnode)
		d.Owner = ownerOf(d.Devnode)
		d.Hidraw = hidrawNodes(dir)
		devs = append(devs, d)
	}
	sort.Slice(devs, func(i, j int) bool {
		if devs[i].Bus != devs[j].Bus {
			return devs[i].Bus < devs[j].Bus
		}
		return devs[i].SysName < devs[j].SysName
	})
	return devs, nil
}

// canOpen reports whether the current user may open the node read-write.
// access(2) honors ACLs, which is how uaccess grants work.
func canOpen(path string) bool {
	return syscall.Access(path, 0x2|0x4) == nil
}

func ownerOf(path string) string {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return "missing"
	}
	return fmt.Sprintf("%s:%s %04o", userName(st.Uid), groupName(st.Gid), st.Mode&0o777)
}

// hidrawNodes finds /dev/hidrawN nodes belonging to this device's interfaces.
func hidrawNodes(dir string) []Node {
	matches, _ := filepath.Glob(filepath.Join(dir, "*:*", "*", "hidraw", "hidraw*"))
	var nodes []Node
	for _, m := range matches {
		p := "/dev/" + filepath.Base(m)
		nodes = append(nodes, Node{Path: p, Writable: canOpen(p)})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	return nodes
}

var (
	nameMu     sync.Mutex
	userCache  = map[uint32]string{}
	groupCache = map[uint32]string{}
)

func userName(uid uint32) string  { return lookupID("/etc/passwd", uid, userCache) }
func groupName(gid uint32) string { return lookupID("/etc/group", gid, groupCache) }

func lookupID(file string, id uint32, cache map[uint32]string) string {
	nameMu.Lock()
	defer nameMu.Unlock()
	if n, ok := cache[id]; ok {
		return n
	}
	n := strconv.Itoa(int(id))
	if f, err := os.Open(file); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			p := strings.Split(sc.Text(), ":")
			if len(p) > 2 && p[2] == n {
				n = p[0]
				break
			}
		}
		f.Close()
	}
	cache[id] = n
	return n
}

// usb.ids lookup, for devices that don't report string descriptors.

type idDB struct {
	vendors  map[string]string
	products map[string]string
}

var (
	idsOnce sync.Once
	ids     idDB
)

func usbIDs() *idDB {
	idsOnce.Do(func() {
		ids = idDB{vendors: map[string]string{}, products: map[string]string{}}
		for _, p := range []string{"/usr/share/hwdata/usb.ids", "/usr/share/misc/usb.ids"} {
			f, err := os.Open(p)
			if err != nil {
				continue
			}
			sc := bufio.NewScanner(f)
			vendor := ""
			for sc.Scan() {
				line := sc.Text()
				if strings.HasPrefix(line, "C ") { // device classes follow; done
					break
				}
				if len(line) < 6 || line[0] == '#' {
					continue
				}
				if line[0] != '\t' {
					vendor = strings.ToLower(line[:4])
					ids.vendors[vendor] = strings.TrimSpace(line[4:])
				} else if line[1] != '\t' && len(line) > 6 {
					ids.products[vendor+":"+strings.ToLower(line[1:5])] = strings.TrimSpace(line[5:])
				}
			}
			f.Close()
			break
		}
	})
	return &ids
}

func (db *idDB) vendor(vid string) string       { return db.vendors[vid] }
func (db *idDB) product(vid, pid string) string { return db.products[vid+":"+pid] }
