package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
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

// homeModelForTest points $HOME at a settings fixture and builds a real model on
// the home screen (the table). initialModel loads settings from $HOME in --cli
// mode, so the mode column and 1-4 keys are exercised against the fixture rather
// than the real ~/.claude/settings.json.
func homeModelForTest(t *testing.T, settingsJSON string, cliMode bool, skills []skill) (model, string) {
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

	var m tea.Model = initialModel(skills, cliMode)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m.(model), path
}

func TestHomeSetModeSavesImmediately(t *testing.T) {
	m, path := homeModelForTest(t, `{"skillOverrides":{}}`, true, twoSkills)
	if m.settings == nil {
		t.Fatal("the cli home screen should load settings at startup")
	}

	m = applyKey(t, m, "4") // alpha (first row) -> off, saved immediately
	if got := m.settings.mode("alpha"); got != modeOff {
		t.Errorf("alpha in memory = %q, want off", got)
	}
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.mode("alpha") != modeOff {
		t.Error("a home mode change should be saved to disk immediately")
	}
}

func TestHomeSetToCurrentModeWritesNothing(t *testing.T) {
	m, path := homeModelForTest(t, `{"skillOverrides":{}}`, true, twoSkills)
	m = applyKey(t, m, "1") // alpha is already on (absent)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.overrides["alpha"]; ok {
		t.Error("setting an already-on skill to on should not write an override")
	}
}

func TestHomeQuitDoesNotPrompt(t *testing.T) {
	m, _ := homeModelForTest(t, `{"skillOverrides":{}}`, true, twoSkills)
	next, _ := m.Update(keyMsg("q"))
	if !next.(model).quitting {
		t.Error("q on the home screen should quit immediately (no save prompt)")
	}
}

func TestHomeModeKeysInertInDesktopMode(t *testing.T) {
	m, _ := homeModelForTest(t, `{"skillOverrides":{}}`, false, []skill{{name: "alpha"}})
	if m.settings != nil {
		t.Error("desktop mode should not load skill settings")
	}
	m = applyKey(t, m, "4") // inert
	if m.view != viewSkills {
		t.Error("mode keys should do nothing on the desktop home screen")
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
// narrow the home table, not just accept typed input.
func TestHomeFilterNarrowsList(t *testing.T) {
	skills := []skill{
		{name: "alpha", description: "the first one"},
		{name: "beta", description: "the second one"},
		{name: "gamma", description: "the third one"},
	}
	m, _ := homeModelForTest(t, `{"skillOverrides":{}}`, true, skills)

	var next tea.Model = m
	next, _ = next.Update(keyMsg("/")) // open the filter
	m = next.(model)

	var cmd tea.Cmd
	for _, r := range "beta" {
		next, cmd = m.Update(keyMsg(string(r)))
		m = next.(model)
	}
	m = runCmdTree(t, m, cmd) // apply the async filter

	vis := m.skillList.VisibleItems()
	if len(vis) != 1 {
		t.Fatalf("expected 1 visible item after filtering \"beta\", got %d", len(vis))
	}
	if it, ok := vis[0].(skillRow); !ok || it.s.name != "beta" {
		t.Errorf("expected the visible match to be beta, got %#v", vis[0])
	}
}

func TestHomeDigitDuringFilterDoesNotSetMode(t *testing.T) {
	m, _ := homeModelForTest(t, `{"skillOverrides":{}}`, true, twoSkills)
	m = applyKey(t, m, "/") // start filtering
	m = applyKey(t, m, "4") // alpha is on; if this leaked to mode-setting it would flip to off
	if m.settings.mode("alpha") != modeOn {
		t.Error("a digit typed into the filter must not set a mode")
	}
}

// enterFileScreenForTest builds a home model against a fixture, then drives into
// the file screen for the first skill (via enter).
func enterFileScreenForTest(t *testing.T, settingsJSON string, skills []skill) (model, string) {
	t.Helper()
	m, path := homeModelForTest(t, settingsJSON, true, skills)
	mm := applyKey(t, m, "enter")
	if mm.view != viewFiles {
		t.Fatalf("enter did not open the file screen (view=%d)", mm.view)
	}
	return mm, path
}

func TestFileScreenSetModeSavesImmediately(t *testing.T) {
	skills := []skill{
		{name: "alpha", dir: t.TempDir()},
		{name: "beta", dir: t.TempDir()},
	}
	m, path := enterFileScreenForTest(t, `{"skillOverrides":{}}`, skills)
	if m.settings == nil {
		t.Fatal("entering the file screen in --cli mode should have settings loaded")
	}

	m = applyKey(t, m, "4") // alpha (the open skill) -> off, saved immediately
	if m.settings.mode("alpha") != modeOff {
		t.Errorf("alpha in memory = %q, want off", m.settings.mode("alpha"))
	}
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.mode("alpha") != modeOff {
		t.Error("a file-screen mode change should be saved to disk immediately")
	}
}

func TestFileScreenTitleShowsMode(t *testing.T) {
	skills := []skill{{name: "alpha", dir: t.TempDir()}}
	m, _ := enterFileScreenForTest(t, `{"skillOverrides":{"alpha":"off"}}`, skills)
	if !strings.Contains(m.fileList.Title, "off") {
		t.Errorf("file-screen title should show the current mode, got %q", m.fileList.Title)
	}
}

func TestFileScreenModeKeysInertInDesktopMode(t *testing.T) {
	skills := []skill{{name: "alpha", dir: t.TempDir()}}
	m, _ := homeModelForTest(t, `{"skillOverrides":{}}`, false, skills)
	mm := applyKey(t, m, "enter")
	if mm.view != viewFiles {
		t.Fatalf("enter should open the file screen, view=%d", mm.view)
	}
	if mm.settings != nil {
		t.Error("desktop mode should not load skill settings")
	}
	mm = applyKey(t, mm, "4") // should be inert, not navigate or crash
	if mm.view != viewFiles {
		t.Error("mode keys should do nothing in desktop mode")
	}
}

// skillFilterNames runs skillFilter over rows and returns the matching skill
// names in the order the filter ranked them.
func skillFilterNames(term string, rows []skillRow) []string {
	targets := make([]string, len(rows))
	for i, r := range rows {
		targets[i] = r.FilterValue()
	}
	var names []string
	for _, rank := range skillFilter(term, targets) {
		names = append(names, rows[rank.Index].s.name)
	}
	return names
}

func TestSkillFilter(t *testing.T) {
	rows := []skillRow{
		{s: skill{name: "shift:review", description: "Review a pull request made by an automated upgrade"}},
		{s: skill{name: "wait-for-it", description: "Waits until a port is open"}},
		{s: skill{name: "ait-recap", description: "Generate a recap of ait work"}},
		{s: skill{name: "cupcake-migration", description: "Move older apps onto our Docker swarm"}},
		{s: skill{name: "ait-usage", description: "Local-first issue tracker"}},
		{s: skill{name: "audience-credibility", description: "Defensible claims"}},
		{s: skill{name: "trainer", description: "Waits patiently, then trait-checks"}},
		{s: skill{name: "docker-pro", description: "Containers"}},
	}

	cases := []struct {
		term string
		want []string
	}{
		// Name prefix only; no fuzzy scatter-matching across descriptions.
		{"aud", []string{"audience-credibility"}},
		// Prefixes first, then names containing it. trainer's description has
		// "Waits" and "trait", both mid-word, so it does not match.
		{"ait", []string{"ait-recap", "ait-usage", "wait-for-it"}},
		// A word-start description match lands after the name matches.
		{"pull", []string{"shift:review"}},
		// Description matches come after every name match.
		{"docker", []string{"docker-pro", "cupcake-migration"}},
		// Case-insensitive on both sides.
		{"DOCK", []string{"docker-pro", "cupcake-migration"}},
		{"zzz", nil},
	}
	for _, c := range cases {
		t.Run(c.term, func(t *testing.T) {
			got := skillFilterNames(c.term, rows)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("skillFilter(%q) = %v, want %v", c.term, got, c.want)
			}
		})
	}
}
