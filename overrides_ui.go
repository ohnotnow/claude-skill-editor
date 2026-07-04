package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Home-screen table rendering + presentation-mode editing ---
//
// The home screen is a dense one-line-per-skill table. In --cli mode it shows
// name | mode | description and the number keys 1-4 set the selected skill's
// mode, saving immediately (no batch, no prompt — the whole tool is "in and
// out"). In Desktop mode the mode column is dropped and 1-4 do nothing.

var (
	// Mode colours. off is an angry red on purpose — a nudge that a skill you've
	// kept hidden for months might as well be removed. The partial-visibility
	// modes are amber. A fully-on skill is left unstyled so the flagged ones are
	// what catch the eye when skimming.
	styleModeOff     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).Bold(true)
	styleModeLimited = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C"))

	styleRowSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)
	styleRowDesc     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C6C6C"))
)

// skillRow is one home-table row: a skill plus a pointer to the loaded settings
// (nil in Desktop mode) so its mode always reflects the latest immediate save.
type skillRow struct {
	s        skill
	settings *skillSettings
}

// FilterValue matches on name and description both, so "that Laravel one" is
// findable — not just exact names.
func (i skillRow) FilterValue() string { return i.s.name + " " + i.s.description }

// skillTableDelegate renders each skill as a single compact table row. showMode
// adds the (colour-coded) presentation-mode column; showLabel prefixes the
// description with the profile tag (Desktop, multiple profiles only).
type skillTableDelegate struct {
	showMode  bool
	showLabel bool
}

func (d skillTableDelegate) Height() int                         { return 1 }
func (d skillTableDelegate) Spacing() int                        { return 0 }
func (d skillTableDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d skillTableDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(skillRow)
	if !ok {
		return
	}

	const (
		prefixW = 2
		nameW   = 26
		modeW   = 20
		gap     = 2
	)
	used := 2 + prefixW + nameW + gap // right margin + prefix + name + gap
	if d.showMode {
		used += modeW + gap
	}
	descW := m.Width() - used
	if descW < 10 {
		descW = 0 // too narrow for a description column
	}

	name := padRight(truncateRunes(it.s.name, nameW), nameW)
	prefix := "  "
	if index == m.Index() {
		prefix = styleRowSelected.Render("▸ ")
		name = styleRowSelected.Render(name)
	}

	line := prefix + name
	if d.showMode {
		mode := it.settings.mode(it.s.name)
		modeCell := padRight(truncateRunes(string(mode), modeW), modeW)
		line += strings.Repeat(" ", gap) + modeStyle(mode).Render(modeCell)
	}
	if descW > 0 {
		desc := it.s.description
		if d.showLabel {
			desc = "[" + it.s.profile.label + "] " + desc
		}
		line += strings.Repeat(" ", gap) + styleRowDesc.Render(truncateRunes(desc, descW))
	}
	fmt.Fprint(w, line)
}

// modeStyle returns the colour for a mode cell.
func modeStyle(m presentationMode) lipgloss.Style {
	switch m {
	case modeOff:
		return styleModeOff
	case modeNameOnly, modeUserInvocable:
		return styleModeLimited
	default:
		return lipgloss.NewStyle()
	}
}

// truncateRunes shortens s to w visible runes, ending in an ellipsis if cut.
func truncateRunes(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

// padRight pads s with spaces to w visible runes (no-op if already wider).
func padRight(s string, w int) string {
	if n := w - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// presentationModeForKey maps the number keys 1-4 to their modes.
func presentationModeForKey(s string) (presentationMode, bool) {
	switch s {
	case "1":
		return modeOn, true
	case "2":
		return modeNameOnly, true
	case "3":
		return modeUserInvocable, true
	case "4":
		return modeOff, true
	}
	return modeOn, false
}

// commitMode sets a skill's mode and saves immediately, returning a status
// message describing what happened. Shared by the home table and the file
// screen — immediate-save everywhere, no batching or prompts.
func (m model) commitMode(name string, mode presentationMode) string {
	if m.settings == nil {
		return "No settings.json found — cannot set mode"
	}
	if !m.settings.setMode(name, mode) {
		return fmt.Sprintf("Already %s", mode)
	}
	if err := m.settings.save(); err != nil {
		return "Save failed: " + err.Error()
	}
	return fmt.Sprintf("Set to %s (saved)", mode)
}

// setHomeSkillMode sets the highlighted home-table skill's mode. The row's live
// settings pointer means the table re-renders the new mode automatically.
func (m model) setHomeSkillMode(mode presentationMode) (tea.Model, tea.Cmd) {
	item, ok := m.skillList.SelectedItem().(skillRow)
	if !ok {
		return m, nil
	}
	return m, m.skillList.NewStatusMessage(m.commitMode(item.s.name, mode))
}

// setCurrentSkillMode sets the mode of the skill open on the file screen and
// refreshes the title so the change is visible.
func (m model) setCurrentSkillMode(mode presentationMode) (tea.Model, tea.Cmd) {
	if m.current == nil {
		return m, nil
	}
	status := m.commitMode(m.current.name, mode)
	m.fileList.Title = m.fileTitle(m.current.name)
	return m, m.fileList.NewStatusMessage(status)
}

// fileTitle builds the file-screen title, appending the skill's current
// presentation mode when settings are loaded (--cli only).
func (m model) fileTitle(name string) string {
	if m.cliMode && m.settings != nil {
		return fmt.Sprintf("%s  files  ·  %s", name, m.settings.mode(name))
	}
	return fmt.Sprintf("%s  files", name)
}
