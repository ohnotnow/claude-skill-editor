package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Presentation-mode overview (viewOverrides) ---
//
// A third screen (--cli only) that renders every skill as a dense one-line
// table row — name | mode | truncated description — so the whole set is
// skimmable at a glance. Number keys 1-4 set the selected skill's mode directly
// (no cycle — the cycle in Claude Code's own editor is the annoyance this
// avoids). Changes are held in memory and marked dirty; leaving with unsaved
// changes prompts to save.

var (
	stylePromptTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))
	stylePromptHelp  = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA"))

	// Mode colours. off is an angry red on purpose — a nudge that a skill you've
	// kept hidden for months might as well be removed. The partial-visibility
	// modes are amber. A fully-on skill is left unstyled so the flagged ones are
	// what catch the eye when skimming.
	styleModeOff     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).Bold(true)
	styleModeLimited = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C"))

	styleRowSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)
	styleRowDesc     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C6C6C"))
)

// overrideItem is one table row: a skill, plus a pointer to the settings so its
// mode always reflects the latest in-memory edit.
type overrideItem struct {
	name     string
	desc     string
	settings *skillSettings
}

// FilterValue matches on name and description both, so "that Laravel one" is
// findable — not just exact names.
func (i overrideItem) FilterValue() string { return i.name + " " + i.desc }

// overrideDelegate renders each item as a single compact table row.
type overrideDelegate struct{}

func (d overrideDelegate) Height() int                         { return 1 }
func (d overrideDelegate) Spacing() int                        { return 0 }
func (d overrideDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d overrideDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(overrideItem)
	if !ok {
		return
	}

	const (
		prefixW = 2
		nameW   = 26
		modeW   = 20
		gap     = 2
	)
	// Leave a small right margin so a full row never wraps.
	descW := m.Width() - 2 - prefixW - nameW - modeW - gap*2
	if descW < 10 {
		descW = 0 // too narrow for a description column
	}

	mode := it.settings.mode(it.name)
	name := padRight(truncateRunes(it.name, nameW), nameW)
	modeCell := padRight(truncateRunes(string(mode), modeW), modeW)

	prefix := "  "
	if index == m.Index() {
		prefix = styleRowSelected.Render("▸ ")
		name = styleRowSelected.Render(name)
	}
	modeCell = modeStyle(mode).Render(modeCell)

	line := prefix + name + strings.Repeat(" ", gap) + modeCell
	if descW > 0 {
		line += strings.Repeat(" ", gap) + styleRowDesc.Render(truncateRunes(it.desc, descW))
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

// newOverrideList builds the overview list for the given skills and settings.
func newOverrideList(skills []skill, settings *skillSettings, width, height int) list.Model {
	items := make([]list.Item, len(skills))
	for idx, s := range skills {
		items[idx] = overrideItem{name: s.name, desc: s.description, settings: settings}
	}

	l := list.New(items, overrideDelegate{}, width, height)
	l.Title = "Skill presentation modes"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "on")),
			key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "name-only")),
			key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "user-inv")),
			key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "off")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc/q", "back")),
		}
	}
	return l
}

// enterOverrides loads settings.json and switches to the overview screen. On a
// load error it shows a transient status message and stays on the skill list.
func (m model) enterOverrides() (tea.Model, tea.Cmd) {
	settings, err := loadSkillSettings(settingsPath())
	if err != nil {
		return m, m.skillList.NewStatusMessage("Could not load settings: " + err.Error())
	}
	m.settings = settings
	m.dirty = false
	m.confirmingSave = false
	m.overrideList = newOverrideList(m.skills, settings, m.width, m.height)
	m.view = viewOverrides
	return m, nil
}

// updateOverrides handles all key input while on the overview screen. Called
// only when not filtering (the caller passes filter keystrokes to the list).
func (m model) updateOverrides(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c is always a hard bail, saved or not.
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	if m.confirmingSave {
		switch msg.String() {
		case "y", "Y", "enter":
			if err := m.settings.save(); err != nil {
				m.confirmingSave = false
				return m, m.overrideList.NewStatusMessage("Save failed: " + err.Error())
			}
			m.dirty = false
			m.confirmingSave = false
			m.view = viewSkills
			return m, nil
		case "n", "N":
			// Discard: leave without saving. Changes evaporate — settings is
			// reloaded fresh next time the screen is opened.
			m.dirty = false
			m.confirmingSave = false
			m.view = viewSkills
			return m, nil
		case "esc":
			// Cancel the quit; keep editing.
			m.confirmingSave = false
			return m, nil
		}
		return m, nil // swallow everything else while confirming
	}

	if mode, ok := presentationModeForKey(msg.String()); ok {
		return m.setSelectedMode(mode)
	}
	switch msg.String() {
	case "q", "esc":
		if m.dirty {
			m.confirmingSave = true
			return m, nil
		}
		m.view = viewSkills
		return m, nil
	}

	// Anything else (navigation, starting a filter) goes to the list.
	var cmd tea.Cmd
	m.overrideList, cmd = m.overrideList.Update(msg)
	return m, cmd
}

// setSelectedMode sets the highlighted skill's mode, marking the model dirty if
// it actually changed.
func (m model) setSelectedMode(mode presentationMode) (tea.Model, tea.Cmd) {
	item, ok := m.overrideList.SelectedItem().(overrideItem)
	if !ok {
		return m, nil
	}
	if m.settings.setMode(item.name, mode) {
		m.dirty = true
	}
	return m, nil
}

// presentationModeForKey maps the number keys 1-4 to their modes. Shared by the
// overview screen and the per-skill file-screen hotkey.
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

// setCurrentSkillMode sets the mode of the skill currently open on the file
// screen and saves immediately — the file screen is about one skill, so a
// single keypress commits it (unlike the overview, which batches and prompts).
// Shows a transient status line and refreshes the title so the effect is clear.
func (m model) setCurrentSkillMode(mode presentationMode) (tea.Model, tea.Cmd) {
	if m.current == nil {
		return m, nil
	}
	if m.settings == nil {
		return m, m.fileList.NewStatusMessage("No settings.json found — cannot set mode")
	}
	if !m.settings.setMode(m.current.name, mode) {
		return m, m.fileList.NewStatusMessage(fmt.Sprintf("Already %s", mode))
	}
	if err := m.settings.save(); err != nil {
		return m, m.fileList.NewStatusMessage("Save failed: " + err.Error())
	}
	m.fileList.Title = m.fileTitle(m.current.name)
	return m, m.fileList.NewStatusMessage(fmt.Sprintf("Set to %s (saved)", mode))
}

// fileTitle builds the file-screen title, appending the skill's current
// presentation mode when settings are loaded (--cli only).
func (m model) fileTitle(name string) string {
	if m.cliMode && m.settings != nil {
		return fmt.Sprintf("%s  files  ·  %s", name, m.settings.mode(name))
	}
	return fmt.Sprintf("%s  files", name)
}

// overrideConfirmView is the "save changes?" prompt shown when leaving the
// overview with unsaved edits.
func (m model) overrideConfirmView() string {
	body := lipgloss.JoinVertical(
		lipgloss.Center,
		stylePromptTitle.Render("Save changes to skill presentation modes?"),
		"",
		stylePromptHelp.Render("y/enter  save     n  discard     esc  keep editing"),
	)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}
