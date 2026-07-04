package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func applyKey(t *testing.T, m tea.Model, s string) model {
	t.Helper()
	next, _ := m.Update(keyMsg(s))
	mm, ok := next.(model)
	if !ok {
		t.Fatalf("Update did not return a model")
	}
	return mm
}

var twoSkills = []skill{{name: "alpha"}, {name: "beta"}}

// enterOverviewForTest points $HOME at a fixture, then drives a real model
// through startup and the `m` keypress so the whole entry path (including
// enterOverrides + loadSkillSettings) is exercised against the fixture rather
// than the real ~/.claude/settings.json. Returns the model on the overview
// screen and the fixture path.
func enterOverviewForTest(t *testing.T, settingsJSON string, skills []skill) (model, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(path, []byte(settingsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	var m tea.Model = initialModel(skills, true)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	mm := applyKey(t, m, "m")
	if mm.view != viewOverrides {
		t.Fatalf("pressing m did not enter the overview (view=%d)", mm.view)
	}
	return mm, path
}

func TestOverrideSetMarksDirty(t *testing.T) {
	m, _ := enterOverviewForTest(t, `{"skillOverrides":{}}`, twoSkills)
	if m.dirty {
		t.Fatal("a fresh overview should not be dirty")
	}
	m = applyKey(t, m, "4") // alpha (first row) -> off
	if !m.dirty {
		t.Error("setting a mode should mark the model dirty")
	}
	if got := m.settings.mode("alpha"); got != modeOff {
		t.Errorf("alpha = %q, want off", got)
	}
}

func TestOverrideSetToCurrentIsNoOp(t *testing.T) {
	m, _ := enterOverviewForTest(t, `{"skillOverrides":{}}`, twoSkills)
	m = applyKey(t, m, "1") // alpha is already on
	if m.dirty {
		t.Error("setting a skill to its current mode should not mark dirty")
	}
}

func TestOverrideCleanQuitReturnsToSkills(t *testing.T) {
	m, _ := enterOverviewForTest(t, `{"skillOverrides":{}}`, twoSkills)
	m = applyKey(t, m, "q")
	if m.view != viewSkills {
		t.Errorf("a clean quit should return to the skill list, view=%d", m.view)
	}
	if m.confirmingSave {
		t.Error("a clean quit should not raise the save prompt")
	}
}

func TestOverrideDirtyQuitPromptsThenSaves(t *testing.T) {
	m, path := enterOverviewForTest(t, `{"skillOverrides":{}}`, twoSkills)
	m = applyKey(t, m, "4") // alpha -> off, now dirty
	m = applyKey(t, m, "q") // should prompt, not leave
	if !m.confirmingSave || m.view != viewOverrides {
		t.Fatalf("a dirty quit should prompt and stay on the overview")
	}
	m = applyKey(t, m, "y") // save
	if m.view != viewSkills || m.dirty || m.confirmingSave {
		t.Errorf("after save: view=%d dirty=%v confirming=%v", m.view, m.dirty, m.confirmingSave)
	}
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.mode("alpha") != modeOff {
		t.Error("save did not persist alpha=off to the file")
	}
}

func TestOverrideDiscardDoesNotPersist(t *testing.T) {
	m, path := enterOverviewForTest(t, `{"skillOverrides":{}}`, twoSkills)
	m = applyKey(t, m, "4") // alpha -> off, dirty
	m = applyKey(t, m, "q")
	m = applyKey(t, m, "n") // discard
	if m.view != viewSkills {
		t.Errorf("discard should return to the skill list, view=%d", m.view)
	}
	s, _ := loadSkillSettings(path)
	if s.mode("alpha") != modeOn {
		t.Error("discard should not have written a change to the file")
	}
}

func TestOverrideEscCancelsConfirm(t *testing.T) {
	m, _ := enterOverviewForTest(t, `{"skillOverrides":{}}`, twoSkills)
	m = applyKey(t, m, "4")   // dirty
	m = applyKey(t, m, "q")   // prompt
	m = applyKey(t, m, "esc") // cancel the prompt
	if m.confirmingSave {
		t.Error("esc should dismiss the save prompt")
	}
	if m.view != viewOverrides {
		t.Error("esc at the prompt should keep us on the overview")
	}
	if !m.dirty {
		t.Error("esc should keep the unsaved change pending")
	}
}

func TestOverrideDigitDuringFilterDoesNotSetMode(t *testing.T) {
	m, _ := enterOverviewForTest(t, `{"skillOverrides":{}}`, twoSkills)
	m = applyKey(t, m, "/") // start filtering
	m = applyKey(t, m, "4") // alpha is on; if this leaked to mode-setting it would flip to off
	if m.dirty {
		t.Error("a digit typed into the filter must not change a mode")
	}
	if m.settings.mode("alpha") != modeOn {
		t.Error("filtering keystroke leaked into mode-setting")
	}
}

// runCmdTree runs the command tree returned by an Update, feeding each message
// back through the model until it settles. Timer commands (e.g. cursor blink)
// block if called synchronously, so each is run with a short timeout and
// skipped if it doesn't return promptly — the filter's filterItems command
// returns instantly, which is the one we care about.
func runCmdTree(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 50; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(150 * time.Millisecond):
			continue // timer/blink command — not what we're draining for
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, bc := range batch {
				queue = append(queue, bc)
			}
			continue
		}
		next, ncmd := m.Update(msg)
		m = next.(model)
		if ncmd != nil {
			queue = append(queue, ncmd)
		}
	}
	return m
}

// Regression test for the dropped-FilterMatchesMsg bug: filtering must actually
// narrow the overview list, not just accept typed input.
func TestOverrideFilterNarrowsList(t *testing.T) {
	skills := []skill{
		{name: "alpha", description: "the first one"},
		{name: "beta", description: "the second one"},
		{name: "gamma", description: "the third one"},
	}
	m, _ := enterOverviewForTest(t, `{"skillOverrides":{}}`, skills)

	var next tea.Model = m
	next, _ = next.Update(keyMsg("/")) // open the filter
	m = next.(model)

	var cmd tea.Cmd
	for _, r := range "beta" {
		next, cmd = m.Update(keyMsg(string(r)))
		m = next.(model)
	}
	m = runCmdTree(t, m, cmd) // apply the async filter

	vis := m.overrideList.VisibleItems()
	if len(vis) != 1 {
		t.Fatalf("expected 1 visible item after filtering \"beta\", got %d", len(vis))
	}
	if it, ok := vis[0].(overrideItem); !ok || it.name != "beta" {
		t.Errorf("expected the visible match to be beta, got %#v", vis[0])
	}
}
