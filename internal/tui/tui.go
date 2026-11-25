// Package tui is the interactive Bubble Tea front end: a filter sidebar, a
// sortable results table, and a detail pane.
package tui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/itxkarthik/jobhunt/internal/app"
	"github.com/itxkarthik/jobhunt/internal/browser"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/search"
	"github.com/itxkarthik/jobhunt/internal/store"
)

// Run starts the TUI and blocks until the user quits.
// Run starts the TUI; newOnly starts it showing only new jobs.
func Run(a *app.App, f model.Filters, newOnly bool) error {
	m := New(a, f)
	m.newOnly = newOnly
	_, err := tea.NewProgram(m).Run()
	return err
}

type pane int

const (
	paneFilters pane = iota
	paneTable
	paneDetail
)

var sortModes = []string{"score", "date", "company"}

// option is one toggle in the filter sidebar.
type option struct {
	group string // country | city | type | mode | area
	value string
}

// sidebarFor lists the filter toggles. Cities come from the filters and the
// profile, since there are too many to list them all.
func sidebarFor(f model.Filters, profileCities []string) []option {
	var out []option
	add := func(group string, vals ...string) {
		for _, v := range vals {
			out = append(out, option{group, v})
		}
	}
	add("country", "IN", "US", "JP", "GB", "DE", "SG", "remote-global")
	var cities []string
	for _, c := range append(slices.Clone(f.Cities), profileCities...) {
		if c = model.CanonicalCity(c); c != "" && !slices.Contains(cities, c) {
			cities = append(cities, c)
		}
	}
	add("city", cities...)
	add("type", "internship", "new-grad", "full-time", "part-time", "contract")
	add("mode", "remote", "hybrid", "onsite")
	add("area", "backend", "full-stack", "systems", "devops", "ml-ai", "data-engineering",
		"frontend", "mobile", "hpc-gpu", "security", "embedded")
	return out
}

type progressMsg struct {
	gen int
	r   search.Report
}

type resultMsg struct {
	gen int
	res *search.Result
	err error
}

// Model is the TUI state.
type Model struct {
	app     *app.App
	filters model.Filters
	side    []option // the filter sidebar's toggles

	all      []model.Job // the last search's results
	jobs     []model.Job // the rows shown: all, or only the new ones
	statuses map[string]store.Status
	// fresh holds the jobs that were new in any search since the TUI
	// started. Every search marks its results seen, so after a refetch
	// they'd no longer be new; fresh keeps their ★ and the new-only view.
	fresh   map[string]bool
	newOnly bool
	reports []search.Report
	total   int

	table  table.Model
	detail viewport.Model
	input  textinput.Model

	focus    pane
	cursor   int // sidebar row
	sortIdx  int
	editing  bool
	showHelp bool

	loading  bool
	gen      int
	ch       chan tea.Msg
	progress []string
	flash    string
	err      error

	width, height int

	// search runs one query; swapped out in tests.
	search func(ctx context.Context, f model.Filters, refresh bool, progress func(search.Report)) (*search.Result, error)
}

func profileCities(a *app.App) []string {
	if a == nil || a.Config == nil {
		return nil
	}
	return a.Config.Profile.Cities
}

// New builds the initial model; the first fetch starts in Init.
func New(a *app.App, f model.Filters) *Model {
	t := table.New(table.WithFocused(true))
	st := table.DefaultStyles()
	st.Header = st.Header.BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).Bold(true)
	st.Selected = st.Selected.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62")).Bold(false)
	t.SetStyles(st)
	in := textinput.New()
	in.Prompt = "keywords: "
	in.Placeholder = "Go, Kubernetes, Rust"
	return &Model{
		app: a, filters: f, side: sidebarFor(f, profileCities(a)), table: t, input: in, focus: paneTable,
		detail: viewport.New(viewport.WithWidth(40), viewport.WithHeight(10)), statuses: map[string]store.Status{}, fresh: map[string]bool{},
		search: func(ctx context.Context, f model.Filters, refresh bool, progress func(search.Report)) (*search.Result, error) {
			eng := a.Engine()
			eng.Refresh, eng.Progress = refresh, progress
			return eng.Run(ctx, f)
		},
	}
}

func (m *Model) Init() tea.Cmd { return m.fetch(false) }

func (m *Model) fetch(refresh bool) tea.Cmd {
	m.gen++
	gen := m.gen
	ch := make(chan tea.Msg, 64)
	m.ch, m.loading, m.progress, m.err = ch, true, nil, nil
	f, run := m.filters, m.search
	go func() {
		res, err := run(context.Background(), f, refresh, func(r search.Report) { ch <- progressMsg{gen, r} })
		ch <- resultMsg{gen, res, err}
		close(ch)
	}()
	return wait(ch)
}

func wait(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case progressMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		r := msg.r
		m.progress = append(m.progress, fmt.Sprintf("%s:%s", r.Source, r.Status))
		return m, wait(m.ch)
	case resultMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.all, m.reports, m.total = msg.res.Jobs, msg.res.Reports, msg.res.Total
		for i := range m.all {
			j := &m.all[i]
			if j.New {
				m.fresh[j.Key] = true
			}
			j.New = m.fresh[j.Key]
		}
		if st, err := m.app.Store.StatusMap(context.Background()); err == nil {
			m.statuses = st
		}
		m.applyView()
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		switch msg.String() {
		case "enter":
			m.editing = false
			m.input.Blur()
			m.filters.Keywords = splitList(m.input.Value())
			return m, m.fetch(false)
		case "esc":
			m.editing = false
			m.input.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	m.flash = ""
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
		m.updateDetail()
		return m, nil
	case "tab":
		m.setFocus((m.focus + 1) % 3)
		return m, nil
	case "shift+tab":
		m.setFocus((m.focus + 2) % 3)
		return m, nil
	case "/":
		m.editing = true
		m.input.SetValue(strings.Join(m.filters.Keywords, ", "))
		m.input.CursorEnd()
		return m, m.input.Focus()
	case "r":
		return m, m.fetch(true)
	case "n":
		m.newOnly = !m.newOnly
		m.applyView()
		m.flash = "showing all jobs"
		if m.newOnly {
			m.flash = "showing new jobs only"
		}
		return m, nil
	case "s":
		m.sortIdx = (m.sortIdx + 1) % len(sortModes)
		m.sortJobs()
		m.refreshRows()
		m.flash = "sorted by " + sortModes[m.sortIdx]
		return m, nil
	case "o":
		if j := m.selected(); j != nil {
			if err := browser.Open(j.URL); err != nil {
				m.flash = "open: " + err.Error()
			} else {
				m.flash = "opened " + j.URL
			}
		}
		return m, nil
	case "S":
		m.track(store.Saved)
		return m, nil
	case "a":
		m.track(store.Applied)
		return m, nil
	case "h":
		m.hide()
		return m, nil
	}

	switch m.focus {
	case paneFilters:
		switch msg.String() {
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(len(m.side)-1, m.cursor+1)
		case "space", "enter", "x":
			m.toggle(m.side[m.cursor])
			return m, m.fetch(false)
		}
		return m, nil
	case paneTable:
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		m.updateDetail()
		return m, cmd
	default:
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}
}

func (m *Model) setFocus(p pane) {
	m.focus = p
	if p == paneTable {
		m.table.Focus()
	} else {
		m.table.Blur()
	}
}

func (m *Model) selected() *model.Job {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.jobs) {
		return nil
	}
	return &m.jobs[i]
}

func (m *Model) track(st store.Status) {
	j := m.selected()
	if j == nil {
		return
	}
	ctx := context.Background()
	if err := m.app.Store.SaveJob(ctx, j); err != nil {
		m.flash = "error: " + err.Error()
		return
	}
	if err := m.app.Store.SetStatus(ctx, j.Key, st, ""); err != nil {
		m.flash = "error: " + err.Error()
		return
	}
	m.statuses[j.Key] = st
	m.flash = fmt.Sprintf("%s → %s", trunc(j.Title, 40), st)
	m.refreshRows()
}

func (m *Model) hide() {
	j := m.selected()
	if j == nil {
		return
	}
	if err := m.app.Store.Hide(context.Background(), j.Key); err != nil {
		m.flash = "error: " + err.Error()
		return
	}
	m.flash = "hidden: " + trunc(j.Title, 40) + "  (jobhunt search --show-hidden to see it)"
	key := j.Key
	i := m.table.Cursor()
	m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
	m.all = slices.DeleteFunc(m.all, func(j model.Job) bool { return j.Key == key })
	m.refreshRows()
}

// applyView picks the rows to show from the last results, then sorts them.
func (m *Model) applyView() {
	m.jobs = slices.Clone(m.all)
	if m.newOnly {
		m.jobs = slices.DeleteFunc(m.jobs, func(j model.Job) bool { return !j.New })
	}
	m.sortJobs()
	m.refreshRows()
}

// toggle flips one sidebar option in the filters.
func (m *Model) toggle(o option) {
	f := &m.filters
	switch o.group {
	case "country":
		f.Countries = flip(f.Countries, o.value)
	case "city":
		f.Cities = flip(f.Cities, o.value)
	case "type":
		f.Types = flipT(f.Types, model.JobType(o.value))
	case "mode":
		f.Modes = flipT(f.Modes, model.WorkMode(o.value))
	case "area":
		f.Areas = flipT(f.Areas, model.Area(o.value))
	}
}

func (m *Model) isOn(o option) bool {
	f := m.filters
	switch o.group {
	case "country":
		return slices.Contains(f.Countries, o.value)
	case "city":
		return slices.Contains(f.Cities, o.value)
	case "type":
		return slices.Contains(f.Types, model.JobType(o.value))
	case "mode":
		return slices.Contains(f.Modes, model.WorkMode(o.value))
	case "area":
		return slices.Contains(f.Areas, model.Area(o.value))
	}
	return false
}

func (m *Model) sortJobs() {
	switch sortModes[m.sortIdx] {
	case "company":
		sort.SliceStable(m.jobs, func(i, j int) bool {
			return strings.ToLower(m.jobs[i].Company) < strings.ToLower(m.jobs[j].Company)
		})
	default:
		search.Sort(m.jobs, sortModes[m.sortIdx])
	}
}

// layout sizes panes for the current terminal.
func (m *Model) layout() {
	if m.width == 0 {
		return
	}
	side, det, tbl := m.widths()
	_ = side
	h := m.bodyHeight() - 2
	m.table.SetWidth(tbl - 2)
	m.table.SetHeight(h)
	m.detail.SetWidth(det - 4)
	m.detail.SetHeight(h)
	m.refreshRows()
}

func (m *Model) widths() (side, detail, tbl int) {
	side = 24
	detail = max(34, (m.width-side)/3)
	tbl = max(20, m.width-side-detail)
	return
}

func (m *Model) bodyHeight() int { return max(5, m.height-2) }

// column is one results-table column; cell gives its text for a job.
type column struct {
	title       string
	width, min  int
	drop        int // order in which narrow terminals drop it; 0 = always shown
	cell        func(m *Model, j *model.Job, now time.Time) string
	isTitleCell bool
}

// tableColumns lists the results-table columns, left to right. Title takes
// whatever width the others leave.
var tableColumns = []column{
	{title: "", width: 1, min: 1, cell: func(_ *Model, j *model.Job, _ time.Time) string {
		if j.New {
			return "★"
		}
		return ""
	}},
	{title: "Scr", width: 3, min: 3, cell: func(_ *Model, j *model.Job, _ time.Time) string { return fmt.Sprintf("%.0f", j.Score) }},
	{title: "Title", isTitleCell: true, cell: func(_ *Model, j *model.Job, _ time.Time) string { return j.Title }},
	{title: "Company", width: 16, min: 10, drop: 5, cell: func(_ *Model, j *model.Job, _ time.Time) string { return j.Company }},
	{title: "Location", width: 12, min: 9, drop: 4, cell: func(m *Model, j *model.Job, _ time.Time) string { return model.ShortLocation(j, m.filters.Countries) }},
	{title: "Pay", width: 10, min: 10, drop: 3, cell: func(_ *Model, j *model.Job, _ time.Time) string { return j.Pay() }},
	{title: "Age", width: 4, min: 4, drop: 2, cell: func(_ *Model, j *model.Job, now time.Time) string { return app.Since(j.PostedAt, now) }},
	{title: "Status", width: 9, min: 9, drop: 1, cell: func(m *Model, j *model.Job, _ time.Time) string { return string(m.statuses[j.Key]) }},
}

// minTitle is the narrowest Title column before other columns shrink or drop.
const minTitle = 16

// fitColumns returns the width of each of tableColumns for inner cells of
// table width. A shown column costs its width plus 2 cells of padding. When
// Title would be narrower than minTitle, Company and Location shrink to their
// minimums, then columns are hidden (width 0) in their drop order: Status,
// Age, Pay, Location, Company. The details pane still shows all of them, and
// the header never wraps. Hidden columns keep their place, because the table
// indexes columns by row cell and skips width-0 ones.
func fitColumns(inner int) []int {
	widths := make([]int, len(tableColumns))
	titleAt := 0
	for i, c := range tableColumns {
		widths[i] = c.width
		if c.isTitleCell {
			titleAt = i
		}
	}
	used := func() int {
		n := 0
		for i, w := range widths {
			if i == titleAt {
				n += 2
			} else if w > 0 {
				n += w + 2
			}
		}
		return n
	}
	if inner-used() < minTitle {
		for i, c := range tableColumns {
			widths[i] = c.min
		}
	}
	for d := 1; inner-used() < minTitle && d <= 5; d++ {
		for i, c := range tableColumns {
			if c.drop == d {
				widths[i] = 0
			}
		}
	}
	widths[titleAt] = max(4, inner-used())
	return widths
}

func (m *Model) refreshRows() {
	_, _, tw := m.widths()
	widths := fitColumns(tw - 2) // table width inside the pane border
	tcols := make([]table.Column, len(tableColumns))
	for i, c := range tableColumns {
		tcols[i] = table.Column{Title: c.title, Width: widths[i]}
	}
	m.table.SetColumns(tcols)
	now := time.Now()
	rows := make([]table.Row, len(m.jobs))
	for i := range m.jobs {
		row := make(table.Row, len(tableColumns))
		for k, c := range tableColumns {
			if widths[k] > 0 {
				row[k] = trunc(c.cell(m, &m.jobs[i], now), widths[k])
			}
		}
		rows[i] = row
	}
	m.table.SetRows(rows)
	if c := m.table.Cursor(); c < 0 || c >= len(rows) {
		m.table.SetCursor(max(0, min(c, len(rows)-1)))
	}
	m.updateDetail()
}

func (m *Model) updateDetail() {
	w := max(10, m.detail.Width())
	if m.showHelp {
		m.detail.SetContent(lipgloss.NewStyle().Width(w).Render(help))
		return
	}
	j := m.selected()
	if j == nil {
		m.detail.SetContent(dim.Render("No job selected."))
		return
	}
	var b strings.Builder
	b.WriteString(titleStyle.Width(w).Render(j.Title) + "\n")
	b.WriteString(j.Company + "\n\n")
	row := func(k, v string) {
		if v != "" {
			b.WriteString(label.Render(fmt.Sprintf("%-9s", k)) + " " + v + "\n")
		}
	}
	row("Location", model.FullLocation(j))
	row("Mode", string(j.WorkMode))
	row("Type", string(j.JobType))
	row("Level", string(j.Experience))
	row("Areas", joinAreas(j.Areas))
	row("Pay", j.Pay())
	if j.VisaSponsorship != nil {
		row("Visa", map[bool]string{true: "sponsors", false: "no sponsorship"}[*j.VisaSponsorship])
	}
	row("Posted", app.Since(j.PostedAt, time.Now())+" ago")
	row("Score", fmt.Sprintf("%.0f", j.Score))
	row("Sources", strings.Join(j.Sources, ", "))
	row("Key", j.Key)
	row("Status", string(m.statuses[j.Key]))
	b.WriteString("\n" + lipgloss.NewStyle().Width(w).Render(j.URL) + "\n\n")
	b.WriteString(lipgloss.NewStyle().Width(w).Render(strings.ReplaceAll(j.Description, "\t", "  ")))
	m.detail.SetContent(b.String())
	m.detail.GotoTop()
}

func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "jobhunt"
	return v
}

func (m *Model) render() string {
	if m.width == 0 {
		return "loading…"
	}
	side, det, tbl := m.widths()
	h := m.bodyHeight() - 2

	var sb strings.Builder
	group := ""
	for i, o := range m.side {
		if o.group != group {
			if group != "" {
				sb.WriteString("\n")
			}
			group = o.group
			sb.WriteString(label.Render(strings.ToUpper(group)) + "\n")
		}
		box := "[ ]"
		if m.isOn(o) {
			box = "[x]"
		}
		line := box + " " + o.value
		if m.focus == paneFilters && i == m.cursor {
			line = cursorStyle.Render(line)
		}
		sb.WriteString(line + "\n")
	}
	sideView := lipgloss.NewStyle().Height(h).MaxHeight(h).Render(scrollTo(sb.String(), m.cursorLine(), h))

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		// lipgloss v2 sizes include the border.
		box(m.focus == paneFilters, side, h+2).Render(sideView),
		box(m.focus == paneTable, tbl, h+2).Render(lipgloss.NewStyle().MaxHeight(h).Render(m.table.View())),
		box(m.focus == paneDetail, det, h+2).Padding(0, 1).Render(m.detail.View()),
	)
	return lipgloss.JoinVertical(lipgloss.Left, m.header(), body, m.footer())
}

func (m *Model) header() string {
	status := fmt.Sprintf("%d jobs (of %d fetched) · sort: %s", len(m.jobs), m.total, sortModes[m.sortIdx])
	if m.newOnly {
		status += " · new only"
	}
	if m.loading {
		status = fmt.Sprintf("fetching… %d/%d sources", len(m.progress), len(m.app.Sources))
	}
	if m.err != nil {
		status = errStyle.Render("error: " + m.err.Error())
	}
	kw := ""
	if len(m.filters.Keywords) > 0 {
		kw = " · kw: " + strings.Join(m.filters.Keywords, ",")
	}
	return trunc(titleStyle.Render("jobhunt")+"  "+status+kw, m.width+20)
}

func (m *Model) footer() string {
	if m.editing {
		return m.input.View()
	}
	if m.flash != "" {
		return flashStyle.Render(trunc(m.flash, m.width))
	}
	return dim.Render(trunc("tab pane · ↑↓ move · space toggle filter · / keywords · s sort · n new only · o open · S save · a applied · h hide · r refetch · ? help · q quit", m.width))
}

// cursorLine is the sidebar line the cursor is on (group headers and blank
// separators included), for scrolling.
func (m *Model) cursorLine() int {
	line, group := 0, ""
	for i, o := range m.side {
		if o.group != group {
			if group != "" {
				line++
			}
			group = o.group
			line++
		}
		if i == m.cursor {
			return line
		}
		line++
	}
	return line
}

func scrollTo(s string, line, h int) string {
	lines := strings.Split(s, "\n")
	start := 0
	if line >= h {
		start = line - h + 1
	}
	end := min(len(lines), start+h)
	return strings.Join(lines[start:end], "\n")
}

const help = `Keys

  tab / shift+tab   switch pane (filters, results, detail)
  ↑ ↓ / j k         move; scroll the detail pane
  space / enter     toggle a filter (filters pane) and re-search
  /                 edit keywords (comma separated), enter to search
  s                 cycle sort: score → date → company
  n                 show only new jobs (★), or all again
  o                 open the job in your browser
  S                 save to tracker
  a                 mark applied
  h                 hide the job from future results
  r                 refetch, bypassing the cache
  ?                 toggle this help
  q                 quit

★ marks jobs not seen in any earlier search.
Track the rest of the pipeline (oa, interview, offer,
rejected) with: jobhunt tracker set <key> <status>`

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	label       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	dim         = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62"))
	flashStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

func box(focused bool, w, h int) lipgloss.Style {
	c := lipgloss.Color("240")
	if focused {
		c = lipgloss.Color("205")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c).
		Width(w).Height(h).MaxHeight(h)
}

func joinAreas(as []model.Area) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = string(a)
	}
	return strings.Join(s, ", ")
}

func splitList(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func trunc(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func flipT[T comparable](xs []T, x T) []T {
	for i, v := range xs {
		if v == x {
			return append(append([]T(nil), xs[:i]...), xs[i+1:]...)
		}
	}
	return append(xs, x)
}

func flip(xs []string, x string) []string { return flipT(xs, x) }
