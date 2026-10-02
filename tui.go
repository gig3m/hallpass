package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	sTab    = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("8"))
	sTabOn  = lipgloss.NewStyle().Padding(0, 1).Bold(true).Reverse(true)
	sOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	sBad    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	sWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	sDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	sSel    = lipgloss.NewStyle().Background(lipgloss.Color("236")).Bold(true)
	sHead   = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true)
	sStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
)

const maxEvents = 6

type tickMsg time.Time

type appliedMsg struct {
	desc string
	err  error
}

type event struct {
	at   time.Time
	add  bool
	text string
}

type model struct {
	tab       int // 0 devices, 1 rules
	all       []Device
	view      []Device
	idx       RuleIndex
	entries   []Entry
	cur       [2]int
	showHubs  bool
	known     map[string]Device
	events    []event
	status    string
	busy      bool
	confirm   string // match pending revoke confirmation
	w, h      int
	firstScan bool
	loadErr   error
}

func newModel() model {
	m := model{known: map[string]Device{}, firstScan: true}
	m.reloadRules()
	m.rescan()
	return m
}

func (m model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) reloadRules() {
	m.idx = LoadRuleIndex()
	m.entries, m.loadErr = LoadEntries()
}

// rescan re-reads sysfs and logs plug/unplug events against the last scan.
func (m *model) rescan() {
	devs, err := Scan()
	if err != nil {
		m.status = "scan: " + err.Error()
		return
	}
	now := time.Now()
	seen := map[string]bool{}
	for _, d := range devs {
		seen[d.Key()] = true
		if _, ok := m.known[d.Key()]; !ok && !m.firstScan && !d.IsRootHub() {
			m.logEvent(event{at: now, add: true, text: fmt.Sprintf("%s  %s  %s", d.ID(), d.SysName, d.Label())})
		}
		m.known[d.Key()] = d
	}
	for k, d := range m.known {
		if !seen[k] {
			if !d.IsRootHub() {
				m.logEvent(event{at: now, add: false, text: fmt.Sprintf("%s  %s  %s", d.ID(), d.SysName, d.Label())})
			}
			delete(m.known, k)
		}
	}
	m.firstScan = false
	m.all = devs
	m.filter()
}

func (m *model) logEvent(e event) {
	m.events = append([]event{e}, m.events...)
	if len(m.events) > maxEvents {
		m.events = m.events[:maxEvents]
	}
}

func (m *model) filter() {
	var sel string
	if d, ok := m.selected(); ok {
		sel = d.SysName
	}
	m.view = m.view[:0]
	for _, d := range m.all {
		if d.IsRootHub() || (d.IsHub() && !m.showHubs) {
			continue
		}
		m.view = append(m.view, d)
	}
	// Keep the cursor on the same device across rescans.
	for i, d := range m.view {
		if d.SysName == sel {
			m.cur[0] = i
		}
	}
	m.clamp()
}

func (m *model) clamp() {
	n := [2]int{len(m.view), len(m.entries)}
	for t := range m.cur {
		if m.cur[t] >= n[t] {
			m.cur[t] = n[t] - 1
		}
		if m.cur[t] < 0 {
			m.cur[t] = 0
		}
	}
}

func (m model) selected() (Device, bool) {
	if m.cur[0] < len(m.view) {
		return m.view[m.cur[0]], true
	}
	return Device{}, false
}

func (m model) entryFor(d Device) (Entry, bool) {
	for _, e := range m.entries {
		if e.Covers(d) {
			return e, true
		}
	}
	return Entry{}, false
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		m.rescan()
		return m, tick()
	case appliedMsg:
		m.busy = false
		if msg.err != nil {
			m.status = sBad.Render(fmt.Sprintf("%s failed: %v", msg.desc, msg.err))
		} else {
			m.status = msg.desc
		}
		m.reloadRules()
		m.rescan()
		m.clamp()
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm != "" {
		match := m.confirm
		m.confirm = ""
		if k.String() == "y" {
			var strip []string
			for _, e := range m.entries {
				if e.Match() == match {
					strip = NodesFor(e, m.all)
				}
			}
			return m.apply(WithoutEntry(m.entries, match), match[:4], strip, "revoked "+match)
		}
		m.status = "kept " + match
		return m, nil
	}
	switch k.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "tab", "shift+tab", "left", "right":
		m.tab = 1 - m.tab
		m.status = ""
	case "up", "k":
		m.cur[m.tab]--
		m.clamp()
	case "down", "j":
		m.cur[m.tab]++
		m.clamp()
	case "home", "g":
		m.cur[m.tab] = 0
	case "end", "G":
		m.cur[m.tab] = 1 << 30
		m.clamp()
	case "H":
		m.showHubs = !m.showHubs
		m.filter()
	case "r":
		m.reloadRules()
		m.rescan()
		m.status = "rescanned"
	case "a", "v":
		if m.tab != 0 || m.busy {
			break
		}
		d, ok := m.selected()
		if !ok {
			break
		}
		e := NewEntry(d, k.String() == "v")
		return m.apply(WithEntry(m.entries, e), d.Vendor, nil, "allowed "+e.Match()+" "+e.Label)
	case "d", "x", "delete":
		if m.busy {
			break
		}
		var match string
		if m.tab == 1 && m.cur[1] < len(m.entries) {
			match = m.entries[m.cur[1]].Match()
		} else if d, ok := m.selected(); ok && m.tab == 0 {
			if e, ok := m.entryFor(d); ok {
				match = e.Match()
			}
		}
		if match == "" {
			m.status = "no hallpass grant to remove here"
			break
		}
		m.confirm = match
	}
	return m, nil
}

// apply runs the privileged install. With cached/passwordless sudo it runs in
// the background; otherwise the terminal is handed to sudo for the prompt.
func (m model) apply(entries []Entry, vendor string, strip []string, desc string) (tea.Model, tea.Cmd) {
	if m.loadErr != nil {
		m.status = sBad.Render("can't read " + ManagedFile + ": " + m.loadErr.Error())
		return m, nil
	}
	cmd, cleanup, err := ApplyCmd(entries, []string{vendor}, strip)
	if err != nil {
		m.status = sBad.Render(err.Error())
		return m, nil
	}
	m.busy = true
	m.status = "applying…"
	if SudoReady() {
		return m, func() tea.Msg {
			defer cleanup()
			out, err := cmd.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
			}
			time.Sleep(200 * time.Millisecond)
			return appliedMsg{desc: desc, err: err}
		}
	}
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		cleanup()
		return appliedMsg{desc: desc, err: err}
	})
}

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	var b strings.Builder
	tabs := []string{"Devices", "Grants"}
	b.WriteString(sTitle.Render("hallpass") + "  ")
	for i, t := range tabs {
		if i == m.tab {
			b.WriteString(sTabOn.Render(t))
		} else {
			b.WriteString(sTab.Render(t))
		}
	}
	b.WriteString("\n\n")

	var body, foot string
	if m.tab == 0 {
		body = m.viewDevices()
		foot = "a allow device · v allow vendor · d revoke · H hubs · tab grants · q quit"
	} else {
		body = m.viewGrants()
		foot = "d revoke · tab devices · q quit"
	}
	b.WriteString(body)

	if m.confirm != "" {
		b.WriteString("\n" + sWarn.Render(fmt.Sprintf("revoke %s? y/n", m.confirm)))
	} else if m.status != "" {
		b.WriteString("\n" + sStatus.Render(m.status))
	}
	b.WriteString("\n" + sDim.Render(foot))
	return b.String()
}

// window returns the slice of n rows to show so cur stays visible.
func window(n, cur, height int) (int, int) {
	if height < 1 {
		height = 1
	}
	start := 0
	if cur >= height {
		start = cur - height + 1
	}
	end := start + height
	if end > n {
		end = n
	}
	return start, end
}

func (m model) viewDevices() string {
	var b strings.Builder
	detail := m.viewDetail()
	events := m.viewEvents()
	listH := m.h - 2 - 1 - lipgloss.Height(detail) - lipgloss.Height(events) - 4
	if listH < 3 {
		listH = 3
	}

	b.WriteString(sHead.Render(fmt.Sprintf("   %-9s  %-9s  %-22s  %s", "ID", "PORT", "GRANT", "DEVICE")) + "\n")
	if len(m.view) == 0 {
		b.WriteString(sDim.Render("   no devices") + "\n")
	}
	start, end := window(len(m.view), m.cur[0], listH)
	for i := start; i < end; i++ {
		d := m.view[i]
		mark := sBad.Render("✗")
		if d.Writable {
			mark = sOK.Render("✓")
		} else if d.HidOnly() {
			mark = sWarn.Render("◐")
		}
		grant, gstyle := m.grantFor(d)
		label := trunc(d.Label(), m.w-52)
		if d.IsHub() {
			label += sDim.Render(" (hub)")
		}
		row := fmt.Sprintf(" %s %-9s  %-9s  %s  %s", mark, d.ID(), d.SysName, gstyle.Render(fmt.Sprintf("%-22s", trunc(grant, 22))), label)
		if i == m.cur[0] {
			row = sSel.Render(padTo(row, m.w))
		}
		b.WriteString(row + "\n")
	}
	for i := end - start; i < listH; i++ {
		b.WriteString("\n")
	}
	b.WriteString("\n" + detail + "\n" + events)
	return b.String()
}

// grantFor names what gives (or fails to give) access: hallpass, another
// rule file, or a late uaccess rule that silently does nothing.
func (m model) grantFor(d Device) (string, lipgloss.Style) {
	if e, ok := m.entryFor(d); ok {
		if e.Product == "" {
			return "hallpass (vendor)", sOK
		}
		return "hallpass", sOK
	}
	refs := m.idx.For(d)
	for _, r := range refs {
		if r.Late() {
			return "⚠ " + r.Base(), sWarn
		}
	}
	if len(refs) > 0 {
		return refs[0].Base(), sDim
	}
	return "—", sDim
}

func (m model) viewDetail() string {
	d, ok := m.selected()
	if !ok {
		return ""
	}
	var lines []string
	acc := sBad.Render("root only")
	if d.Writable {
		acc = sOK.Render("you can open it")
	} else if d.HidOnly() {
		acc = sWarn.Render("root only (hidraw is open: WebHID works, WebUSB doesn't)")
	}
	lines = append(lines, fmt.Sprintf("%s  %s  %s", d.Devnode, sDim.Render(d.Owner), acc))
	if len(d.Hidraw) > 0 {
		var hs []string
		for _, n := range d.Hidraw {
			mark := sBad.Render("✗")
			if n.Writable {
				mark = sOK.Render("✓")
			}
			hs = append(hs, mark+" "+n.Path)
		}
		lines = append(lines, "hidraw  "+strings.Join(hs, "  "))
	}
	if d.Serial != "" {
		lines = append(lines, sDim.Render("serial "+d.Serial))
	}
	for _, r := range m.idx.For(d) {
		l := "rule  " + r.String()
		if r.Late() {
			l = sWarn.Render(l + "  ⚠ sorts after " + seatLate + ": tags uaccess but grants nothing")
		}
		lines = append(lines, l)
	}
	if !d.Writable {
		if _, ok := m.entryFor(d); ok {
			lines = append(lines, sWarn.Render("granted but not applied yet; re-plug the device, or check you're on the active seat (loginctl)"))
		}
	}
	return sHead.Render("─ "+d.Label()+" ") + "\n" + strings.Join(lines, "\n")
}

func (m model) viewEvents() string {
	var b strings.Builder
	b.WriteString(sHead.Render("─ hotplug") + "\n")
	if len(m.events) == 0 {
		b.WriteString(sDim.Render("  waiting for plug/unplug…"))
	}
	for i, e := range m.events {
		sign := sOK.Render("+")
		if !e.add {
			sign = sBad.Render("−")
		}
		b.WriteString(fmt.Sprintf("  %s %s %s", sDim.Render(e.at.Format("15:04:05")), sign, e.text))
		if i < len(m.events)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m model) viewGrants() string {
	var b strings.Builder
	b.WriteString(sDim.Render("  "+ManagedFile) + "\n\n")
	if m.loadErr != nil {
		return b.String() + sBad.Render("  "+m.loadErr.Error()) + "\n"
	}
	if len(m.entries) == 0 {
		return b.String() + sDim.Render("  no grants yet. Select a device on the Devices tab and press a.") + "\n"
	}
	b.WriteString(sHead.Render(fmt.Sprintf("  %-10s  %-10s  %-9s  %s", "MATCH", "ADDED", "PLUGGED", "LABEL")) + "\n")
	listH := m.h - 8
	start, end := window(len(m.entries), m.cur[1], listH)
	for i := start; i < end; i++ {
		e := m.entries[i]
		n := 0
		for _, d := range m.all {
			if e.Covers(d) {
				n++
			}
		}
		plugged := sDim.Render(fmt.Sprintf("%-9s", "—"))
		if n > 0 {
			plugged = sOK.Render(fmt.Sprintf("%-9s", fmt.Sprintf("%d now", n)))
		}
		row := fmt.Sprintf("  %-10s  %-10s  %s  %s", e.Match(), e.Added, plugged, e.Label)
		if i == m.cur[1] {
			row = sSel.Render(padTo(row, m.w))
		}
		b.WriteString(row + "\n")
	}
	return b.String()
}

func trunc(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func padTo(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
