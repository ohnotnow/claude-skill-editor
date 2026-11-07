package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTempSettings writes content to a settings.json in a fresh temp dir and
// returns its path. Tests never touch the real ~/.claude/settings.json.
func writeTempSettings(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func parseRaw(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return m
}

// jsonEqual compares two raw JSON values by their decoded structure, ignoring
// formatting and key order.
func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	return reflect.DeepEqual(av, bv)
}

func TestMode(t *testing.T) {
	path := writeTempSettings(t, `{
  "skillOverrides": {
    "explicit-on": "on",
    "quiet": "name-only",
    "hidden": "user-invocable-only",
    "gone": "off"
  }
}`)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	cases := map[string]presentationMode{
		"absent-skill": modeOn, // absent is treated as on
		"explicit-on":  modeOn, // a literal "on" is also the on-state
		"quiet":        modeNameOnly,
		"hidden":       modeUserInvocable,
		"gone":         modeOff,
	}
	for skill, want := range cases {
		if got := s.mode(skill); got != want {
			t.Errorf("mode(%q) = %q, want %q", skill, got, want)
		}
	}
}

func TestModeNoOverridesKey(t *testing.T) {
	path := writeTempSettings(t, `{"permissions": {"allow": []}}`)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := s.mode("anything"); got != modeOn {
		t.Errorf("mode with no skillOverrides key = %q, want on", got)
	}
}

func TestSetMode(t *testing.T) {
	path := writeTempSettings(t, `{"skillOverrides": {"foo": "off"}}`)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	t.Run("no-op when unchanged", func(t *testing.T) {
		if s.setMode("foo", modeOff) {
			t.Error("setMode to the same value reported a change")
		}
	})
	t.Run("change reports true", func(t *testing.T) {
		if !s.setMode("foo", modeNameOnly) {
			t.Error("setMode to a new value reported no change")
		}
		if s.overrides["foo"] != "name-only" {
			t.Errorf("foo = %q, want name-only", s.overrides["foo"])
		}
	})
	t.Run("setting on deletes the entry", func(t *testing.T) {
		if !s.setMode("foo", modeOn) {
			t.Error("setMode off->on reported no change")
		}
		if _, ok := s.overrides["foo"]; ok {
			t.Error("setting a skill to on should delete its entry")
		}
	})
	t.Run("setting on for an absent skill is a no-op", func(t *testing.T) {
		if s.setMode("never-seen", modeOn) {
			t.Error("setMode on for an absent skill reported a change")
		}
	})
}

func TestSavePreservesSiblings(t *testing.T) {
	const src = `{
  "permissions": {"allow": ["Bash(git status:*)"], "deny": ["Bash(git push *)"]},
  "hooks": {"Stop": [{"command": "run-me"}]},
  "skillOverrides": {"foo": "off"},
  "someNumber": 42,
  "nested": {"a": {"b": [1, 2, 3]}}
}`
	path := writeTempSettings(t, src)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	s.setMode("bar", modeNameOnly)
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	orig := parseRaw(t, []byte(src))
	saved := parseRaw(t, readFile(t, path))
	for key, origVal := range orig {
		if key == skillOverridesKey {
			continue
		}
		if !jsonEqual(t, origVal, saved[key]) {
			t.Errorf("key %q changed across save:\n orig: %s\nsaved: %s", key, origVal, saved[key])
		}
	}

	var ov map[string]string
	if err := json.Unmarshal(saved[skillOverridesKey], &ov); err != nil {
		t.Fatalf("parse saved overrides: %v", err)
	}
	if ov["foo"] != "off" || ov["bar"] != "name-only" {
		t.Errorf("overrides after save = %v, want foo=off bar=name-only", ov)
	}
}

func TestSaveRemovesEmptyOverrides(t *testing.T) {
	path := writeTempSettings(t, `{"permissions": {}, "skillOverrides": {"foo": "off"}}`)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	s.setMode("foo", modeOn) // deletes foo, leaving overrides empty
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	saved := parseRaw(t, readFile(t, path))
	if _, ok := saved[skillOverridesKey]; ok {
		t.Error("an emptied skillOverrides should be dropped from the file")
	}
	if _, ok := saved["permissions"]; !ok {
		t.Error("permissions should be preserved")
	}
}

func TestSaveCreatesOverridesWhenNeeded(t *testing.T) {
	path := writeTempSettings(t, `{"permissions": {"allow": []}}`)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !s.setMode("foo", modeOff) {
		t.Fatal("expected setMode to report a change")
	}
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	saved := parseRaw(t, readFile(t, path))
	var ov map[string]string
	if err := json.Unmarshal(saved[skillOverridesKey], &ov); err != nil {
		t.Fatalf("parse overrides: %v", err)
	}
	if ov["foo"] != "off" {
		t.Errorf("foo = %q, want off", ov["foo"])
	}
}

// Guards the SetEscapeHTML(false) choice: a hook command containing shell
// redirection / logical-and must survive verbatim, not turn into > etc.
// Also confirms the output is indented (the RawMessage re-indent question).
func TestSavePreservesShellCommandsVerbatim(t *testing.T) {
	const cmd = `echo hi > /tmp/out && cat a<b`
	path := writeTempSettings(t, `{
  "hooks": {"Stop": [{"command": "`+cmd+`"}]},
  "skillOverrides": {"foo": "on"}
}`)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	s.setMode("foo", modeOff)
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	text := string(readFile(t, path))
	// This is itself the proof that HTML escaping is off: with the default
	// escaping on, the >, & and < would be unicode-escaped and this literal
	// command substring would not be found.
	if !strings.Contains(text, cmd) {
		t.Errorf("shell command not preserved verbatim; file:\n%s", text)
	}
	if !strings.Contains(text, "\n  \"") {
		t.Errorf("expected indented output; got:\n%s", text)
	}
}

// A file already in two-space style must come back byte-for-byte, apart from
// the one override that changed: keys stay in the user's order, not sorted.
func TestSaveKeepsKeyOrder(t *testing.T) {
	const src = `{
  "permissions": {
    "allow": [
      "Bash(git status:*)"
    ]
  },
  "skillOverrides": {
    "foo": "off"
  },
  "hooks": {},
  "env": []
}
`
	path := writeTempSettings(t, src)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	s.setMode("foo", modeNameOnly)
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	want := strings.Replace(src, `"foo": "off"`, `"foo": "name-only"`, 1)
	if got := string(readFile(t, path)); got != want {
		t.Errorf("saved file differs from expected.\n got:\n%s\nwant:\n%s", got, want)
	}
}

// A brand-new skillOverrides key goes at the end, and stays there on later
// saves rather than drifting.
func TestSaveAppendsNewOverridesKeyAtEnd(t *testing.T) {
	path := writeTempSettings(t, `{"zebra": 1, "apple": 2}`)
	s, err := loadSkillSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	s.setMode("foo", modeOff)
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.setMode("bar", modeOff)
	if err := s.save(); err != nil {
		t.Fatalf("second save: %v", err)
	}

	const want = `{
  "zebra": 1,
  "apple": 2,
  "skillOverrides": {
    "bar": "off",
    "foo": "off"
  }
}
`
	if got := string(readFile(t, path)); got != want {
		t.Errorf("saved file differs from expected.\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := loadSkillSettings(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("expected an error for a missing settings file")
	}
}

func writeSkillMD(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return path
}

func TestParseFrontmatterDescription(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want string
	}{
		{
			name: "inline",
			md:   "---\nname: foo\ndescription: A simple description\n---\nbody",
			want: "A simple description",
		},
		{
			name: "quoted inline",
			md:   "---\nname: foo\ndescription: \"Quoted description\"\n---\n",
			want: "Quoted description",
		},
		{
			name: "folded block",
			md:   "---\nname: foo\ndescription: >\n  A folded description that\n  spans two lines\n---\n",
			want: "A folded description that spans two lines",
		},
		{
			name: "literal block",
			md:   "---\nname: foo\ndescription: |\n  Line one\n  Line two\n---\n",
			want: "Line one Line two",
		},
		{
			name: "folded with chomp indicator",
			md:   "---\nname: foo\ndescription: >-\n  Chomped folded\n  description\n---\n",
			want: "Chomped folded description",
		},
		{
			name: "block ends at the next key",
			md:   "---\ndescription: >\n  Only this line\nname: foo\n---\n",
			want: "Only this line",
		},
		{
			name: "blank line inside a block is skipped",
			md:   "---\ndescription: |\n  First para\n\n  Second para\n---\n",
			want: "First para Second para",
		},
		{
			name: "long inline is truncated",
			md:   "---\ndescription: " + strings.Repeat("x", 200) + "\n---\n",
			want: strings.Repeat("x", 117) + "...",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, desc := parseSkillFrontmatter(writeSkillMD(t, tc.md))
			if desc != tc.want {
				t.Errorf("description = %q, want %q", desc, tc.want)
			}
		})
	}
}

func TestParseFrontmatterName(t *testing.T) {
	name, _ := parseSkillFrontmatter(writeSkillMD(t,
		"---\ndescription: >\n  folded text here\nname: my-skill\n---\n"))
	if name != "my-skill" {
		t.Errorf("name = %q, want my-skill (a block-scalar description must not swallow the name key)", name)
	}
}
