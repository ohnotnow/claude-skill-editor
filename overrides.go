package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// presentationMode is a skill's value in the `skillOverrides` map of
// ~/.claude/settings.json — how (and whether) Claude Code surfaces the skill.
// The confirmed value set and rationale live in the ant ADR (cdse-AkRXV).
type presentationMode string

const (
	modeOn            presentationMode = "on"                  // name + description, auto-invocable
	modeNameOnly      presentationMode = "name-only"           // name only
	modeUserInvocable presentationMode = "user-invocable-only" // hidden from the agent, still in the / menu
	modeOff           presentationMode = "off"                 // hidden entirely
)

// presentationModes lists the modes in increasing order of restriction — this
// is also the order the UI binds to number keys 1-4.
var presentationModes = []presentationMode{modeOn, modeNameOnly, modeUserInvocable, modeOff}

const skillOverridesKey = "skillOverrides"

// settingsPath returns the path to Claude Code's settings file, where
// skillOverrides live. Mirrors the style of cliSkillsDir (main.go).
func settingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// skillSettings is an editable, in-memory view of settings.json that preserves
// every top-level key it doesn't understand. Only skillOverrides is
// interpreted; all other keys (permissions, hooks, …) ride through as raw bytes
// on save, so we never drop or mangle config this tool has no business touching.
type skillSettings struct {
	path      string
	raw       map[string]json.RawMessage // every top-level key, verbatim
	overrides map[string]string          // parsed skillOverrides: skill name -> value
}

// loadSkillSettings reads and parses the settings file at path.
func loadSkillSettings(path string) (*skillSettings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read settings file %s: %w", path, err)
	}

	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}

	overrides := map[string]string{}
	if rawOverrides, ok := raw[skillOverridesKey]; ok {
		if err := json.Unmarshal(rawOverrides, &overrides); err != nil {
			return nil, fmt.Errorf("cannot parse %s in %s: %w", skillOverridesKey, path, err)
		}
	}

	return &skillSettings{path: path, raw: raw, overrides: overrides}, nil
}

// mode returns the presentation mode for a skill. A skill absent from the
// overrides — or one whose value is a literal "on" — is the on-state.
func (s *skillSettings) mode(skill string) presentationMode {
	v, ok := s.overrides[skill]
	if !ok || v == "" || presentationMode(v) == modeOn {
		return modeOn
	}
	return presentationMode(v)
}

// setMode sets a skill's mode in memory and reports whether anything changed.
// Setting a skill to modeOn deletes its entry: absent is treated as "on", so
// there is no point writing a redundant "on" string (see ADR).
func (s *skillSettings) setMode(skill string, m presentationMode) bool {
	if s.mode(skill) == m {
		return false
	}
	if m == modeOn {
		delete(s.overrides, skill)
	} else {
		s.overrides[skill] = string(m)
	}
	return true
}

// save writes the settings back to disk atomically. Only the skillOverrides key
// is rewritten from our parsed map; every other top-level key is preserved as
// it was loaded. When no overrides remain, the key is dropped entirely.
//
// Note: Go's encoding/json marshals map keys alphabetically, so the whole file
// is reordered and reindented on save. This is a deliberately accepted
// trade-off (see ADR) — no data is lost, only key order and whitespace change.
func (s *skillSettings) save() error {
	if len(s.overrides) == 0 {
		delete(s.raw, skillOverridesKey)
	} else {
		encoded, err := json.Marshal(s.overrides)
		if err != nil {
			return fmt.Errorf("cannot encode %s: %w", skillOverridesKey, err)
		}
		s.raw[skillOverridesKey] = encoded
	}

	// Encode with HTML escaping off so preserved shell commands in hooks
	// (which may contain >, &, <) survive verbatim rather than as > etc.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s.raw); err != nil {
		return fmt.Errorf("cannot encode settings: %w", err)
	}

	// Preserve the original file's permission bits.
	perm := os.FileMode(0o644)
	if info, err := os.Stat(s.path); err == nil {
		perm = info.Mode().Perm()
	}

	// Write to a temp file in the same directory, then rename over the
	// original — so a crash mid-write can't leave a half-written settings file.
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has succeeded

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("cannot set permissions on temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("cannot replace settings file: %w", err)
	}
	return nil
}
