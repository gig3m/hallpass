package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Palette from the "Hallpass TUI" design (1b).
var (
	cBg    = lipgloss.Color("#141317")
	cFg    = lipgloss.Color("#ebe6dc")
	cDim   = lipgloss.Color("#7d7787")
	cBrand = lipgloss.Color("#ff7eb6")
	cOK    = lipgloss.Color("#8be28b")
	cWarn  = lipgloss.Color("#ffd166")
	cBad   = lipgloss.Color("#ff6b6b")
	cInfo  = lipgloss.Color("#7cc7ff")
	cSel   = lipgloss.Color("#25222b")
	cLine  = lipgloss.Color("#38343f")
	cChip  = lipgloss.Color("#2b2731")
	cCode  = lipgloss.Color("#1c1a20")
	cFlash = lipgloss.Color("#2c3a2c")
	cLive  = lipgloss.Color("#2f4a35")
	cInk   = lipgloss.Color("#1a1418")
)

const (
	maxEvents = 5
	cardW     = 50 // right panel, borders included
	eventsH   = maxEvents + 2
	spinner   = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
)

func fg(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

var (
	sDim   = fg(cDim)
	sBold  = lipgloss.NewStyle().Bold(true)
	sBrand = fg(cBrand)
	sHead  = fg(cDim).Bold(true)
)

func chip(k string) string {
	return lipgloss.NewStyle().Background(cChip).Foreground(cBrand).Bold(true).Padding(0, 1).Render(k)
}

// seg is a run of text in one color; rows are built from segs so a selected
// or flashing row can carry one background across all of them.
type seg struct {
	t    string
	c    lipgloss.Color
	bold bool
	w    int // fixed width (pad/truncate); 0 = as is
}

func renderSegs(segs []seg, width int, bg lipgloss.Color, bold bool) string {
	var b strings.Builder
	used := 0
	for _, s := range segs {
		t := s.t
		if s.w > 0 {
			t = fit(t, s.w)
		}
		if width > 0 && used+lipgloss.Width(t) > width {
			t = ansi.Truncate(t, width-used, "…")
		}
		st := lipgloss.NewStyle().Foreground(s.c).Bold(s.bold || bold)
		if bg != "" {
			st = st.Background(bg)
		}
		b.WriteString(st.Render(t))
		used += lipgloss.Width(t)
		if width > 0 && used >= width {
			break
		}
	}
	if width > used {
		pad := strings.Repeat(" ", width-used)
		if bg != "" {
			pad = lipgloss.NewStyle().Background(bg).Render(pad)
		}
		b.WriteString(pad)
	}
	return b.String()
}

// fit pads or truncates to exactly w cells.
func fit(s string, w int) string {
	if lipgloss.Width(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// box draws a rounded panel with a title (and optional right meta) set into
// the top border, like the design's fieldset-style panels.
func box(title, meta string, body []string, w, h int, border, titleC lipgloss.Color) []string {
	in := w - 4
	bc := fg(border)
	top := bc.Render("╭─") + " " + fg(titleC).Bold(true).Render(title) + " "
	right := bc.Render("─╮")
	if meta != "" {
		right = " " + meta + " " + right
	}
	fill := max(0, w-lipgloss.Width(top)-lipgloss.Width(right))
	out := []string{top + bc.Render(strings.Repeat("─", fill)) + right}
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		if lipgloss.Width(line) > in {
			line = ansi.Truncate(line, in, "…")
		}
		line += strings.Repeat(" ", in-lipgloss.Width(line))
		out = append(out, bc.Render("│")+" "+line+" "+bc.Render("│"))
	}
	return append(out, bc.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}

// overlay splices a modal into the background, centered.
func overlay(bg []string, modal []string, w int) []string {
	mw := 0
	for _, l := range modal {
		mw = max(mw, lipgloss.Width(l))
	}
	top := max(0, (len(bg)-len(modal))/2)
	left := max(0, (w-mw)/2)
	out := append([]string(nil), bg...)
	for i, l := range modal {
		y := top + i
		if y >= len(out) {
			break
		}
		base := out[y]
		pre := ansi.Truncate(base, left, "")
		pre += strings.Repeat(" ", left-lipgloss.Width(pre))
		post := ansi.TruncateLeft(base, left+mw, "")
		out[y] = pre + l + strings.Repeat(" ", mw-lipgloss.Width(l)) + post
	}
	return out
}

// ---- model ----

type tickMsg time.Time
type spinMsg struct{}
type flashDoneMsg struct{ n int }
type stepMsg struct{ step string }
type applyDoneMsg struct {
	job  job
	err  error
	auth bool // failed on the password
}

type event struct {
	at   time.Time
	add  bool
	d    Device
	note string
	nc   lipgloss.Color
}

type job struct {
	allow   bool
	entry   Entry
	entries []Entry
	strip   []string
}

type applying struct {
	job   job
	steps []string // "@@" markers, in order
	text  []string
	i     int
	ch    chan tea.Msg
}

type model struct {
	tab      int
	all      []Device
	view     []Device
	idx      RuleIndex
	entries  []Entry // sorted by match, as in the file
	cur      [2]int
	showHubs bool
	known    map[string]Device
	events   []event
	status   string
	statusC  lipgloss.Color
	w, h     int
	scanned  bool
	loadErr  error

	user       string
	seat       string
	seatActive bool
	sudoCached bool
	live       bool
	ticks      int

	confirm *job
	pw      *job
	pwBuf   string
	pwErr   string
	apply   *applying
	spin    int
	help    bool
	flash   map[string]bool
	flashN  int
}

func newModel() model {
	m := model{known: map[string]Device{}, user: currentUser(), statusC: cDim, status: "press ? for how passes work"}
	m.seat, m.seatActive = Seat()
	m.sudoCached = SudoReady()
	m.reloadRules()
	m.rescan()
	return m
}

func (m model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) setStatus(t string, c lipgloss.Color) { m.status, m.statusC = t, c }

func (m *model) reloadRules() {
	m.idx = LoadRuleIndex()
	m.entries, m.loadErr = LoadEntries()
	sortEntries(m.entries)
}

func sortEntries(e []Entry) {
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && e[j].Match() < e[j-1].Match(); j-- {
			e[j], e[j-1] = e[j-1], e[j]
		}
	}
}

func (m *model) rescan() {
	devs, err := Scan()
	if err != nil {
		m.setStatus("scan: "+err.Error(), cBad)
		return
	}
	now := time.Now()
	seen := map[string]bool{}
	for _, d := range devs {
		seen[d.Key()] = true
		if _, ok := m.known[d.Key()]; !ok && m.scanned && !d.IsRootHub() {
			note, nc := "no pass", cBad
			if _, ok := m.entryFor(d); ok {
				note, nc = "pass ✓", cOK
			} else if d.Writable {
				note, nc = "open", cDim
			}
			m.logEvent(event{at: now, add: true, d: d, note: note, nc: nc})
		}
		m.known[d.Key()] = d
	}
	for k, d := range m.known {
		if !seen[k] {
			if !d.IsRootHub() {
				m.logEvent(event{at: now, d: d})
			}
			delete(m.known, k)
		}
	}
	m.scanned = true
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
		m.cur[t] = max(0, min(m.cur[t], n[t]-1))
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

func (m model) hubCount() int {
	n := 0
	for _, d := range m.all {
		if d.IsHub() && !d.IsRootHub() {
			n++
		}
	}
	return n
}

func (m model) plugged(e Entry) int {
	n := 0
	for _, d := range m.all {
		if e.Covers(d) {
			n++
		}
	}
	return n
}

func (m model) quitLine() string {
	return fmt.Sprintf("hallpass: %d pass%s in %s", len(m.entries), pluralES(len(m.entries)), ManagedFile)
}

// ---- update ----

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		m.live = !m.live
		m.ticks++
		if m.apply == nil {
			m.rescan()
			if m.ticks%10 == 0 {
				m.sudoCached = SudoReady()
			}
		}
		return m, tick()
	case spinMsg:
		if m.apply == nil {
			return m, nil
		}
		m.spin++
		return m, spin()
	case stepMsg:
		if m.apply == nil {
			return m, nil
		}
		for i, s := range m.apply.steps {
			if s == msg.step {
				m.apply.i = i
			}
		}
		if msg.step == "done" {
			m.apply.i = len(m.apply.steps)
		}
		return m, waitApply(m.apply.ch)
	case applyDoneMsg:
		return m.finish(msg)
	case flashDoneMsg:
		if msg.n == m.flashN {
			m.flash = nil
		}
	case tea.MouseMsg:
		return m.mouse(msg)
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// Layout: header, blank, main panels (mainH), hotplug box, footer.
func (m model) mainH() int     { return max(8, m.h-3-eventsH) }
func (m model) showCard() bool { return m.w >= 100 }

// listTop is the screen row of the first list row; listRows how many fit.
func (m model) listTop() int {
	if m.tab == 0 {
		return 4 // header, blank, border, column header
	}
	return 6 // ... border, path, blank, column header
}
func (m model) listRows() int { return m.mainH() - 1 - (m.listTop() - 2) }

func (m model) mouse(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	if e.Action != tea.MouseActionPress || e.Button != tea.MouseButtonLeft || m.modal() {
		return m, nil
	}
	if e.Y == 0 {
		switch {
		case e.X >= 14 && e.X < 28:
			m.tab = 0
		case e.X >= 28 && e.X < 42:
			m.tab = 1
		}
		return m, nil
	}
	n := len(m.view)
	if m.tab == 1 {
		n = len(m.entries)
	}
	start, _ := window(n, m.cur[m.tab], m.listRows())
	row := e.Y - m.listTop()
	if row >= 0 && row < m.listRows() && start+row < n && (!m.showCard() || e.X < m.w-cardW) {
		m.cur[m.tab] = start + row
	}
	return m, nil
}

func (m model) modal() bool { return m.confirm != nil || m.pw != nil || m.apply != nil || m.help }

func (m model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" && m.apply == nil {
		return m, tea.Quit
	}
	switch {
	case m.apply != nil:
		return m, nil
	case m.pw != nil:
		switch k.Type {
		case tea.KeyEsc:
			m.pw, m.pwBuf, m.pwErr = nil, "", ""
			m.setStatus("cancelled", cDim)
		case tea.KeyBackspace:
			if r := []rune(m.pwBuf); len(r) > 0 {
				m.pwBuf = string(r[:len(r)-1])
			}
			m.pwErr = ""
		case tea.KeyEnter:
			if m.pwBuf == "" {
				m.pwErr = "sudo: a password is required"
				return m, nil
			}
			j, pw := *m.pw, m.pwBuf
			m.pw, m.pwBuf, m.pwErr = nil, "", ""
			return m.run(j, pw)
		case tea.KeyRunes, tea.KeySpace:
			m.pwBuf += string(k.Runes)
			m.pwErr = ""
		}
		return m, nil
	case m.help:
		m.help = false
		return m, nil
	case m.confirm != nil:
		j := *m.confirm
		m.confirm = nil
		if key == "y" {
			return m.start(j)
		}
		m.setStatus("kept "+j.entry.Match(), cDim)
		return m, nil
	}

	switch key {
	case "q", "esc":
		return m, tea.Quit
	case "?":
		m.help = true
	case "tab", "shift+tab", "left", "right":
		m.tab = 1 - m.tab
		m.status = ""
	case "1":
		m.tab = 0
	case "2":
		m.tab = 1
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
		if m.showHubs {
			m.setStatus("showing hubs", cDim)
		} else {
			m.setStatus("hubs hidden", cDim)
		}
	case "r":
		m.reloadRules()
		m.rescan()
		m.sudoCached = SudoReady()
		m.setStatus("rescanned", cInfo)
	case "a", "v":
		if m.tab != 0 {
			break
		}
		d, ok := m.selected()
		if !ok {
			break
		}
		vendor := key == "v"
		if e, ok := m.entryFor(d); ok && (!vendor || e.Product == "") {
			m.setStatus(fmt.Sprintf("%s already has a pass (%s)", d.ID(), e.Match()), cDim)
			break
		}
		e := NewEntry(d, vendor)
		return m.start(job{allow: true, entry: e, entries: WithEntry(m.entries, e)})
	case "d", "x", "delete":
		var e Entry
		var ok bool
		if m.tab == 1 && m.cur[1] < len(m.entries) {
			e, ok = m.entries[m.cur[1]], true
		} else if d, sel := m.selected(); sel && m.tab == 0 {
			e, ok = m.entryFor(d)
		}
		if !ok {
			m.setStatus("no hallpass grant to remove here", cDim)
			break
		}
		m.confirm = &job{entry: e, entries: WithoutEntry(m.entries, e.Match()), strip: NodesFor(e, m.all)}
	}
	return m, nil
}

func (m model) start(j job) (tea.Model, tea.Cmd) {
	if m.loadErr != nil {
		m.setStatus("can't read "+ManagedFile+": "+m.loadErr.Error(), cBad)
		return m, nil
	}
	if m.sudoCached = SudoReady(); !m.sudoCached {
		m.pw = &j
		return m, nil
	}
	return m.run(j, "")
}

// run starts the privileged apply, streaming its "@@ step" markers back as
// stepMsgs. With a password it feeds sudo -S; otherwise it relies on the
// cached ticket (-n), so sudo never touches the TUI's terminal.
func (m model) run(j job, password string) (tea.Model, tea.Cmd) {
	flags := []string{"-n"}
	if password != "" {
		flags = []string{"-S", "-p", ""}
	}
	cmd, cleanup, err := ApplyCmd(j.entries, []string{j.entry.Vendor}, j.strip, flags)
	if err != nil {
		m.setStatus(err.Error(), cBad)
		return m, nil
	}
	a := &applying{job: j, ch: make(chan tea.Msg, 16), i: 1} // staging is done
	add := func(marker, text string) { a.steps = append(a.steps, marker); a.text = append(a.text, text) }
	if j.allow {
		add("stage", "stage "+baseName(ManagedFile))
	} else {
		add("stage", "stage "+baseName(ManagedFile)+" without "+j.entry.Match())
	}
	add("install", "install → "+ManagedFile)
	add("reload", "udevadm control --reload")
	if len(j.strip) > 0 {
		add("strip", fmt.Sprintf("setfacl -x u:%s on %d node%s", m.user, len(j.strip), plural(len(j.strip))))
	}
	add("trigger", "udevadm trigger --attr-match=idVendor="+j.entry.Vendor)
	add("settle", "udevadm settle --timeout=5")
	m.apply = a
	m.spin = 0

	if password != "" {
		cmd.Stdin = strings.NewReader(password + "\n")
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		m.apply = nil
		m.setStatus(err.Error(), cBad)
		return m, nil
	}
	go func() {
		defer cleanup()
		if err := cmd.Start(); err != nil {
			a.ch <- applyDoneMsg{job: j, err: err}
			return
		}
		readSteps(stdout, a.ch)
		err := cmd.Wait()
		time.Sleep(250 * time.Millisecond) // logind applies the ACL just after settle
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			auth := strings.Contains(msg, "incorrect password") || strings.Contains(msg, "password is required") || strings.Contains(msg, "try again")
			if msg == "" {
				msg = err.Error()
			}
			a.ch <- applyDoneMsg{job: j, err: fmt.Errorf("%s", lastLine(msg)), auth: auth}
			return
		}
		a.ch <- applyDoneMsg{job: j}
	}()
	return m, tea.Batch(waitApply(a.ch), spin())
}

func readSteps(r io.Reader, ch chan tea.Msg) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if s, ok := strings.CutPrefix(sc.Text(), "@@ "); ok {
			ch <- stepMsg{step: s}
		}
	}
}

func waitApply(ch chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-ch } }

func spin() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

func (m model) finish(r applyDoneMsg) (tea.Model, tea.Cmd) {
	m.apply = nil
	m.sudoCached = SudoReady()
	e := r.job.entry
	if r.err != nil {
		if r.auth {
			j := r.job
			m.pw, m.pwErr = &j, "sudo: incorrect password"
			return m, nil
		}
		m.setStatus(fmt.Sprintf("%s %s failed: %v", verb(r.job), e.Match(), r.err), cBad)
		m.reloadRules()
		return m, nil
	}
	m.reloadRules()
	m.rescan()
	m.clamp()
	if !r.job.allow {
		m.setStatus("revoked "+e.Match(), cInfo)
		return m, nil
	}
	m.setStatus("★ pass issued · "+e.Match()+" "+e.Label, cOK)
	m.flash = map[string]bool{}
	for _, d := range m.all {
		if e.Covers(d) {
			m.flash[d.SysName] = true
		}
	}
	m.flashN++
	n := m.flashN
	return m, tea.Tick(1400*time.Millisecond, func(time.Time) tea.Msg { return flashDoneMsg{n} })
}

func verb(j job) string {
	if j.allow {
		return "issuing"
	}
	return "revoking"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func pluralES(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

func baseName(p string) string { return p[strings.LastIndex(p, "/")+1:] }

func lastLine(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	return l[len(l)-1]
}

// ---- view ----

func window(n, cur, height int) (int, int) {
	height = max(1, height)
	start := 0
	if cur >= height {
		start = cur - height + 1
	}
	return start, min(n, start+height)
}

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	lines := []string{m.viewHeader(), ""}
	lines = append(lines, m.viewMain()...)
	lines = append(lines, m.viewEvents()...)
	lines = append(lines, m.viewFooter())

	switch {
	case m.apply != nil:
		lines = overlay(lines, m.viewApply(), m.w)
	case m.pw != nil:
		lines = overlay(lines, m.viewPassword(), m.w)
	case m.confirm != nil:
		lines = overlay(lines, m.viewConfirm(), m.w)
	case m.help:
		lines = overlay(lines, m.viewHelp(), m.w)
	}
	return strings.Join(lines, "\n")
}

func (m model) viewHeader() string {
	badge := lipgloss.NewStyle().Background(cBrand).Foreground(cInk).Bold(true).Padding(0, 1).Render("▞ hallpass") + sBrand.Render("▌")
	tab := func(i int, name string, n int) string {
		st := lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(cDim)
		if m.tab == i {
			st = st.Background(cFg).Foreground(cBg)
		}
		return st.Render(fmt.Sprintf("%d %s %d", i+1, name, n))
	}
	left := badge + "  " + fit(tab(0, "Devices", len(m.view)), 14) + fit(tab(1, "Grants", len(m.entries)), 14)

	dot, txt := fg(cOK).Render("●"), fmt.Sprintf("%s · %s active", m.user, m.seat)
	switch {
	case m.seat == "":
		dot, txt = fg(cWarn).Render("●"), m.user+" · no seat, uaccess won't apply"
	case !m.seatActive:
		dot, txt = fg(cWarn).Render("●"), fmt.Sprintf("%s · %s inactive", m.user, m.seat)
	}
	sudo := sDim.Render("○ will ask")
	if m.sudoCached {
		sudo = fg(cOK).Render("● ready")
	}
	right := dot + sDim.Render(" "+txt+"   sudo ") + sudo
	if gap := m.w - lipgloss.Width(left) - lipgloss.Width(right); gap >= 2 {
		return left + strings.Repeat(" ", gap) + right
	}
	return left
}

func (m model) viewMain() []string {
	h := m.mainH()
	listW := m.w
	if m.showCard() {
		listW = m.w - cardW - 1
	}
	var list, card []string
	if m.tab == 0 {
		meta := sDim.Render(fmt.Sprintf("%d hubs hidden · H", m.hubCount()))
		if m.showHubs {
			meta = sDim.Render(fmt.Sprintf("%d hubs shown", m.hubCount()))
		}
		list = box("devices", meta, m.deviceRows(listW-4), listW, h, cBrand, cBrand)
		if m.showCard() {
			card = box("pass", "", m.passCard(cardW-4, h-2), cardW, h, cLine, cDim)
		}
	} else {
		list = box("grants", sDim.Render(fmt.Sprintf("%d pass%s", len(m.entries), pluralES(len(m.entries)))), m.grantRows(listW-4), listW, h, cBrand, cBrand)
		if m.showCard() {
			card = box("rule", "", m.ruleCard(cardW-4, h-2), cardW, h, cLine, cDim)
		}
	}
	if card == nil {
		return list
	}
	out := make([]string, h)
	for i := range out {
		out[i] = list[i] + " " + card[i]
	}
	return out
}

func header(w int, cols ...seg) string {
	for i := range cols {
		cols[i].c = cDim
	}
	return renderSegs(cols, w, "", false)
}

func (m model) deviceRows(w int) []string {
	out := []string{header(w, seg{w: 4}, seg{t: "ID", w: 11}, seg{t: "PORT", w: 9}, seg{t: "GRANT", w: 22}, seg{t: "DEVICE"})}
	if len(m.view) == 0 {
		return append(out, sDim.Render("  no devices"))
	}
	start, end := window(len(m.view), m.cur[0], m.listRows())
	for i := start; i < end; i++ {
		d := m.view[i]
		sel := i == m.cur[0]
		mark, mc := statusMark(d)
		grant, gc := m.grantFor(d)
		bar, hub := "", ""
		if sel {
			bar = "▍"
		}
		if d.IsHub() {
			hub = " (hub)"
		}
		var bg lipgloss.Color
		switch {
		case m.flash[d.SysName]:
			bg = cFlash
		case sel:
			bg = cSel
		}
		out = append(out, renderSegs([]seg{
			{t: bar, c: cBrand, w: 2}, {t: mark, c: mc, w: 2},
			{t: d.ID(), c: cFg, w: 11}, {t: d.SysName, c: cDim, w: 9},
			{t: grant, c: gc, w: 22}, {t: d.Label(), c: cFg}, {t: hub, c: cDim},
		}, w, bg, sel))
	}
	return out
}

func statusMark(d Device) (string, lipgloss.Color) {
	switch {
	case d.Writable:
		return "✓", cOK
	case d.HidOnly():
		return "◐", cWarn
	}
	return "✗", cBad
}

func (m model) grantFor(d Device) (string, lipgloss.Color) {
	if e, ok := m.entryFor(d); ok {
		if e.Product == "" {
			return "hallpass (vendor)", cOK
		}
		return "hallpass", cOK
	}
	refs := m.idx.For(d)
	for _, r := range refs {
		if r.Late() {
			return "⚠ " + r.Base(), cWarn
		}
	}
	if len(refs) > 0 {
		return refs[0].Base(), cDim
	}
	return "—", cDim
}

func (m model) passCard(w, h int) []string {
	d, ok := m.selected()
	if !ok {
		return []string{sDim.Render("nothing selected")}
	}
	e, has := m.entryFor(d)
	refs := m.idx.For(d)
	late := false
	for _, r := range refs {
		late = late || r.Late()
	}

	bannerC, banner, why := cBad, "✗ ROOT ONLY", "only root can open it"
	switch {
	case d.Writable && has:
		kind := "device"
		if e.Product == "" {
			kind = "vendor"
		}
		bannerC, banner, why = cOK, "✓ PASS GRANTED", fmt.Sprintf("you can open it via hallpass %s pass %s", kind, e.Match())
	case d.Writable:
		bannerC, banner, why = cOK, "✓ OPEN", "you can open it via another rule"
	case d.HidOnly():
		bannerC, banner, why = cWarn, "◐ HID ONLY", "hidraw is open: WebHID works, WebUSB doesn't"
	case has:
		bannerC, banner, why = cWarn, "… PASS PENDING", "granted but not applied; re-plug, or check the seat"
	case late:
		why = "a rule exists but grants nothing"
	}
	ticket := ""
	if has {
		ticket = "#" + e.Match()
	}
	bst := lipgloss.NewStyle().Background(bannerC).Foreground(cBg).Bold(true)
	out := []string{
		bst.Render(" " + banner + strings.Repeat(" ", max(1, w-lipgloss.Width(banner)-lipgloss.Width(ticket)-2)) + ticket + " "),
		"",
		sBold.Render(d.Label() + map[bool]string{true: " (hub)"}[d.IsHub()]),
		sDim.Render(fmt.Sprintf("%s · port %s · bus %03d dev %03d", d.ID(), d.SysName, d.Bus, d.Dev)),
		fg(bannerC).Render(why),
		"",
		sHead.Render("NODES"),
	}
	node := func(kind, path string, ok bool) string {
		mark, c, state := "✗", cBad, "locked"
		if ok {
			mark, c, state = "✓", cOK, "open"
		}
		return spread(fg(c).Render(fit(mark, 2))+sDim.Render(fit(kind, 8))+path, fg(c).Render(state), w)
	}
	out = append(out, node("usb", d.Devnode, d.Writable))
	for _, n := range d.Hidraw {
		out = append(out, node("hidraw", n.Path, n.Writable))
	}
	owner := d.Owner
	if users := ACLUsers(d.Devnode); len(users) > 0 {
		owner += "  + acl u:" + strings.Join(users, ",") + " rw-"
	}
	out = append(out, sDim.Render("  "+owner))
	if d.Serial != "" {
		out = append(out, sDim.Render("  serial "+d.Serial))
	}

	works := func(ok bool, t string) string {
		if ok {
			return fg(cOK).Render("✓ " + t)
		}
		return fg(cBad).Render("✗ " + t)
	}
	hid := sDim.Render("· WebHID n/a")
	if len(d.Hidraw) > 0 {
		all := true
		for _, n := range d.Hidraw {
			all = all && n.Writable
		}
		hid = works(all, "WebHID")
	}
	out = append(out, "", sHead.Render("WORKS WITH"), works(d.Writable, "WebUSB")+"  "+works(d.Writable, "libusb / dfu-util")+"  "+hid)

	out = append(out, "", sHead.Render("RULES"))
	if len(refs) == 0 {
		out = append(out, sDim.Render("no udev rule mentions "+d.Vendor))
	}
	for _, r := range refs {
		c, t := cDim, r.String()
		switch {
		case r.Base() == baseName(ManagedFile):
			c = cOK
		case r.Late():
			c, t = cWarn, "⚠ "+t
		}
		out = append(out, fg(c).Render(t))
	}
	if late && !d.Writable {
		for _, l := range wrap("sorts after "+seatLate+", so it tags uaccess but grants nothing. Press a to issue a 70- pass.", w-2) {
			out = append(out, "  "+fg(cWarn).Render(l))
		}
	}

	issued, admit := "—", "—"
	if has {
		issued = e.Added
	}
	if d.Writable {
		admit = m.user + " @ " + orDash(m.seat)
	}
	acts := chip("a") + " allow " + d.ID() + "  " + chip("v") + " vendor " + d.Vendor + ":*"
	if has {
		acts = chip("d") + " revoke " + e.Match()
	}
	return pinBottom(out, []string{
		fg(cLine).Render(strings.Repeat("┄", w)),
		spread(sDim.Render("ADMIT ")+admit, sDim.Render("ISSUED ")+issued, w),
		acts,
	}, h)
}

func (m model) grantRows(w int) []string {
	out := []string{sDim.Render(ManagedFile), ""}
	if m.loadErr != nil {
		return append(out, fg(cBad).Render(m.loadErr.Error()))
	}
	if len(m.entries) == 0 {
		return append(out, sDim.Render("no passes yet. Pick a device on Devices and press a."))
	}
	out = append(out, header(w, seg{w: 2}, seg{t: "MATCH", w: 12}, seg{t: "ADDED", w: 12}, seg{t: "PLUGGED", w: 10}, seg{t: "LABEL"}))
	start, end := window(len(m.entries), m.cur[1], m.listRows())
	for i := start; i < end; i++ {
		e := m.entries[i]
		sel := i == m.cur[1]
		pl, pc := "—", cDim
		if n := m.plugged(e); n > 0 {
			pl, pc = fmt.Sprintf("%d now", n), cOK
		}
		bar := ""
		var bg lipgloss.Color
		if sel {
			bar, bg = "▍", cSel
		}
		out = append(out, renderSegs([]seg{
			{t: bar, c: cBrand, w: 2}, {t: e.Match(), c: cBrand, w: 12}, {t: e.Added, c: cDim, w: 12},
			{t: pl, c: pc, w: 10}, {t: e.Label, c: cFg},
		}, w, bg, sel))
	}
	return out
}

var tokRe = regexp.MustCompile(`([A-Z]+(?:\{[^}]*\})?)(==|\+=)("[^"]*")(, )?`)

func (m model) ruleCard(w, h int) []string {
	if len(m.entries) == 0 {
		return []string{sDim.Render("no pass selected")}
	}
	e := m.entries[m.cur[1]]
	scope := "one product"
	if e.Product == "" {
		scope = "every product from vendor " + e.Vendor
	}
	out := []string{
		sBold.Render(e.Label),
		sDim.Render(fmt.Sprintf("%s · added %s", e.Match(), e.Added)),
		sDim.Render(scope),
		"",
		sHead.Render("WRITTEN TO FILE"),
	}
	code := lipgloss.NewStyle().Background(cCode)
	blank := code.Render(strings.Repeat(" ", w))
	out = append(out, blank)
	for i, l := range e.Lines() {
		segs := []seg{{t: l, c: cDim}}
		if i > 0 {
			segs = nil
			for _, mm := range tokRe.FindAllStringSubmatch(l, -1) {
				vc := cOK
				if strings.HasPrefix(mm[1], "TAG") {
					vc = cBrand
				}
				segs = append(segs, seg{t: mm[1], c: cInfo}, seg{t: mm[2], c: cDim}, seg{t: mm[3], c: vc})
				if mm[4] != "" {
					segs = append(segs, seg{t: mm[4], c: cDim})
				}
			}
		}
		for _, row := range wrapSegs(segs, w-2) {
			out = append(out, code.Render(" ")+renderSegs(row, w-2, cCode, false)+code.Render(" "))
		}
		out = append(out, blank)
	}
	out = append(out, "", sHead.Render("PLUGGED IN NOW"))
	n := 0
	for _, d := range m.all {
		if e.Covers(d) {
			n++
			out = append(out, fg(cOK).Render("✓ ")+fit(d.ID(), 11)+d.Label())
		}
	}
	if n == 0 {
		out = append(out, sDim.Render("nothing connected matches "+e.Match()))
	}
	return pinBottom(out, []string{chip("d") + " revoke " + e.Match()}, h)
}

// wrapSegs breaks a colored run into lines of w cells anywhere, like the
// design's word-break: break-all.
func wrapSegs(segs []seg, w int) [][]seg {
	var rows [][]seg
	var row []seg
	used := 0
	for _, s := range segs {
		r := []rune(s.t)
		for len(r) > 0 {
			if used == w {
				rows, row, used = append(rows, row), nil, 0
			}
			n := min(w-used, len(r))
			row = append(row, seg{t: string(r[:n]), c: s.c})
			used += n
			r = r[n:]
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

func wrap(s string, w int) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) > w:
			out = append(out, line)
			line = word
		default:
			line += " " + word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// pinBottom puts foot at the bottom of an h-line panel body.
func pinBottom(body, foot []string, h int) []string {
	room := max(0, h-len(foot))
	if len(body) > room {
		body = body[:room]
	}
	for len(body) < room {
		body = append(body, "")
	}
	return append(body, foot...)
}

func spread(l, r string, w int) string {
	return l + strings.Repeat(" ", max(1, w-lipgloss.Width(l)-lipgloss.Width(r))) + r
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (m model) viewEvents() []string {
	live := cLive
	if m.live {
		live = cOK
	}
	meta := fg(live).Render("●") + sDim.Render(" watching /sys/bus/usb")
	var body []string
	if len(m.events) == 0 {
		body = append(body, sDim.Render("waiting for plug/unplug…"))
	}
	for i, e := range m.events {
		sign, sc := "+", cOK
		if !e.add {
			sign, sc = "−", cBad
		}
		fgc, dim := cFg, cDim
		if i >= 2 { // older events fade, as in the design
			fgc, dim = cDim, cLine
		}
		body = append(body, renderSegs([]seg{
			{t: e.at.Format("15:04:05") + " ", c: dim}, {t: sign, c: sc, bold: true, w: 2},
			{t: e.d.ID(), c: fgc, w: 11}, {t: e.d.SysName, c: dim, w: 8}, {t: e.d.Label() + "  ", c: fgc},
			{t: e.note, c: e.nc},
		}, m.w-4, "", false))
	}
	return box("hotplug", meta, body, m.w, eventsH, cLine, cDim)
}

func (m model) viewFooter() string {
	type kl struct{ k, l string }
	keys := []kl{{"a", "allow"}, {"v", "vendor"}, {"d", "revoke"}, {"H", "hubs"}, {"tab", "grants"}, {"?", "help"}, {"q", "quit"}}
	if m.showHubs {
		keys[3].l = "hide hubs"
	}
	if m.tab == 1 {
		keys = []kl{{"d", "revoke"}, {"tab", "devices"}, {"?", "help"}, {"q", "quit"}}
	}
	var parts []string
	for _, x := range keys {
		parts = append(parts, chip(x.k)+" "+sDim.Render(x.l))
	}
	left := strings.Join(parts, "  ")
	room := m.w - lipgloss.Width(left) - 2
	if room < 10 {
		return left
	}
	status := fg(m.statusC).Render(ansi.Truncate(m.status, room, "…"))
	return left + strings.Repeat(" ", m.w-lipgloss.Width(left)-lipgloss.Width(status)) + status
}

// ---- modals ----

func modal(title string, c lipgloss.Color, w int, body []string) []string {
	padded := []string{""}
	for _, l := range body {
		padded = append(padded, " "+l)
	}
	padded = append(padded, "")
	return box(title, "", padded, w, len(padded)+2, c, c)
}

func (m model) viewConfirm() []string {
	j := m.confirm
	impact := "nothing connected uses it"
	if n := m.plugged(j.entry); n == 1 {
		impact = "1 connected device loses access right away"
	} else if n > 1 {
		impact = fmt.Sprintf("%d connected devices lose access right away", n)
	}
	return modal("revoke pass", cBad, 60, []string{
		sBold.Render(j.entry.Match() + "  " + j.entry.Label),
		sDim.Render(impact),
		"",
		lipgloss.NewStyle().Background(cBad).Foreground(cBg).Bold(true).Padding(0, 1).Render("y") + " revoke   " +
			lipgloss.NewStyle().Background(cChip).Foreground(cFg).Bold(true).Padding(0, 1).Render("n") + " keep it",
	})
}

func (m model) viewPassword() []string {
	j := m.pw
	what := "issue"
	if !j.allow {
		what = "revoke"
	}
	field := sDim.Render(fmt.Sprintf("[sudo] password for %s: ", m.user)) +
		fg(cInfo).Render(strings.Repeat("•", len([]rune(m.pwBuf)))) +
		lipgloss.NewStyle().Background(cInfo).Render(" ")
	body := []string{
		fmt.Sprintf("%s %s needs sudo", what, j.entry.Match()),
		sDim.Render("writes " + baseName(ManagedFile) + ", reloads udev, retriggers " + j.entry.Vendor),
		"",
		field,
	}
	if m.pwErr != "" {
		body = append(body, fg(cBad).Render(m.pwErr))
	}
	body = append(body, "", sDim.Render("enter to continue · esc cancel"))
	return modal("sudo", cInfo, 64, body)
}

func (m model) viewApply() []string {
	a := m.apply
	title := "issuing pass"
	if !a.job.allow {
		title = "revoking"
	}
	body := []string{sBold.Render(verb(a.job) + " " + a.job.entry.Match() + "  " + a.job.entry.Label), ""}
	sp := []rune(spinner)
	for i, t := range a.text {
		switch {
		case i < a.i:
			body = append(body, fg(cOK).Render("✓  "+t))
		case i == a.i:
			body = append(body, string(sp[m.spin%len(sp)])+"  "+t)
		default:
			body = append(body, sDim.Render("·  "+t))
		}
	}
	bw := 58
	done := bw * a.i / max(1, len(a.text))
	body = append(body, "", lipgloss.NewStyle().Background(cBrand).Render(strings.Repeat(" ", done))+
		lipgloss.NewStyle().Background(cChip).Render(strings.Repeat(" ", bw-done)))
	return modal(title, cBrand, 64, body)
}

func (m model) viewHelp() []string {
	row := func(mark string, c lipgloss.Color, t string) string { return fg(c).Render(fit(mark, 3)) + t }
	key := func(k, l string) string { return fit(sBrand.Bold(true).Render(k)+"  "+l, 30) }
	body := []string{
		row("✓", cOK, "you can open the device"),
		row("◐", cWarn, "only hidraw is open: WebHID works, WebUSB doesn't"),
		row("✗", cBad, "root only"),
		row("⚠", cWarn, "uaccess rule sorts after 73-seat-late.rules, grants nothing"),
		"",
	}
	for _, l := range wrap("Passes are uaccess rules in "+ManagedFile+". logind gives the active seat user an ACL; nobody joins a group. Vendor passes (v) cover devices that change product ID between modes, like a LEGO hub in DFU.", 64) {
		body = append(body, sDim.Render(l))
	}
	body = append(body, "",
		key("a", "allow device")+key("v", "allow whole vendor"),
		key("d", "revoke")+key("H", "show hubs"),
		key("tab", "switch tab")+key("r", "rescan"),
		"",
		sDim.Render("any key to close"))
	return modal("how passes work", cBrand, 70, body)
}
