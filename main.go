package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Skill discovery ---

type skillProfile struct {
	label     string // short UUID prefix for display
	uuid      string // full outer UUID
	skillsDir string // full path to the skills/ directory
}

type skill struct {
	name        string
	description string
	dir         string // full path to the skill directory
	profile     skillProfile
}

type skillFile struct {
	name    string // relative path within the skill dir
	absPath string
}

func skillsBaseDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "Claude", "local-agent-mode-sessions", "skills-plugin")
	case "linux":
		// Try XDG_CONFIG_HOME first, then ~/.config
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "Claude", "local-agent-mode-sessions", "skills-plugin")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "Claude", "local-agent-mode-sessions", "skills-plugin")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "Claude", "local-agent-mode-sessions", "skills-plugin")
	}
}

func discoverSkills() ([]skill, error) {
	base := skillsBaseDir()
	outerEntries, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("cannot read skills directory %s: %w", base, err)
	}

	var skills []skill
	for _, outer := range outerEntries {
		if !outer.IsDir() {
			continue
		}
		outerPath := filepath.Join(base, outer.Name())
		innerEntries, err := os.ReadDir(outerPath)
		if err != nil {
			continue
		}
		for _, inner := range innerEntries {
			if !inner.IsDir() {
				continue
			}
			skillsDir := filepath.Join(outerPath, inner.Name(), "skills")
			if _, err := os.Stat(skillsDir); err != nil {
				continue
			}
			profile := skillProfile{
				label:     outer.Name()[:8],
				uuid:      outer.Name(),
				skillsDir: skillsDir,
			}
			skillEntries, err := os.ReadDir(skillsDir)
			if err != nil {
				continue
			}
			for _, se := range skillEntries {
				if !se.IsDir() {
					continue
				}
				skillDir := filepath.Join(skillsDir, se.Name())
				skillMD := filepath.Join(skillDir, "SKILL.md")
				if _, err := os.Stat(skillMD); err != nil {
					continue
				}
				name, desc := parseSkillFrontmatter(skillMD)
				if name == "" {
					name = se.Name()
				}
				skills = append(skills, skill{
					name:        name,
					description: desc,
					dir:         skillDir,
					profile:     profile,
				})
			}
		}
	}

	sort.Slice(skills, func(i, j int) bool {
		if skills[i].name != skills[j].name {
			return skills[i].name < skills[j].name
		}
		return skills[i].profile.label < skills[j].profile.label
	})

	return skills, nil
}

func parseSkillFrontmatter(path string) (name, description string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inFrontmatter := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			if inFrontmatter {
				break // end of frontmatter
			}
			inFrontmatter = true
			continue
		}
		if !inFrontmatter {
			continue
		}
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		} else if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			// Strip surrounding quotes if present
			description = strings.Trim(description, "\"'")
			// Truncate long descriptions for display
			if len(description) > 120 {
				description = description[:117] + "..."
			}
		}
	}
	return name, description
}

func listSkillFiles(skillDir string) []skillFile {
	var files []skillFile
	filepath.Walk(skillDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(skillDir, path)
		files = append(files, skillFile{name: rel, absPath: path})
		return nil
	})
	// Put SKILL.md first
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].name == "SKILL.md" {
			return true
		}
		if files[j].name == "SKILL.md" {
			return false
		}
		return false
	})
	return files
}

func editorCmd() string {
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	return "nano"
}

// --- Bubble Tea model ---

type viewState int

const (
	viewSkills viewState = iota
	viewFiles
)

// List item adapters
type skillItem struct{ s skill }

func (i skillItem) Title() string {
	return i.s.name
}
func (i skillItem) Description() string {
	desc := i.s.description
	if desc == "" {
		desc = "(no description)"
	}
	return fmt.Sprintf("[%s] %s", i.s.profile.label, desc)
}
func (i skillItem) FilterValue() string { return i.s.name + " " + i.s.description }

type fileItem struct{ f skillFile }

func (i fileItem) Title() string       { return i.f.name }
func (i fileItem) Description() string { return "" }
func (i fileItem) FilterValue() string { return i.f.name }

type model struct {
	view      viewState
	skillList list.Model
	fileList  list.Model
	skills    []skill
	current   *skill
	width     int
	height    int
	quitting  bool
}

type editorFinishedMsg struct{ err error }

func initialModel(skills []skill) model {
	// Determine if we have multiple profiles
	profiles := map[string]bool{}
	for _, s := range skills {
		profiles[s.profile.uuid] = true
	}

	items := make([]list.Item, len(skills))
	for i, s := range skills {
		items[i] = skillItem{s}
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("#7C3AED")).
		BorderLeftForeground(lipgloss.Color("#7C3AED"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("#A78BFA")).
		BorderLeftForeground(lipgloss.Color("#7C3AED"))

	title := "Claude Desktop Skills"
	if len(profiles) > 1 {
		title = fmt.Sprintf("Claude Desktop Skills (%d profiles)", len(profiles))
	}

	sl := list.New(items, delegate, 80, 24)
	sl.Title = title
	sl.SetShowStatusBar(true)
	sl.SetFilteringEnabled(true)
	sl.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open skill")),
		}
	}

	return model{
		view:      viewSkills,
		skillList: sl,
		skills:    skills,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.skillList.SetSize(msg.Width, msg.Height)
		if m.current != nil {
			m.fileList.SetSize(msg.Width, msg.Height)
		}
		return m, nil

	case editorFinishedMsg:
		return m, nil

	case tea.KeyMsg:
		// Don't intercept keys while filtering
		if m.view == viewSkills && m.skillList.FilterState() == list.Filtering {
			var cmd tea.Cmd
			m.skillList, cmd = m.skillList.Update(msg)
			return m, cmd
		}
		if m.view == viewFiles && m.fileList.FilterState() == list.Filtering {
			var cmd tea.Cmd
			m.fileList, cmd = m.fileList.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if m.view == viewFiles {
				m.view = viewSkills
				m.current = nil
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit

		case "esc":
			if m.view == viewFiles {
				m.view = viewSkills
				m.current = nil
				return m, nil
			}

		case "enter":
			switch m.view {
			case viewSkills:
				if item, ok := m.skillList.SelectedItem().(skillItem); ok {
					s := item.s
					m.current = &s
					m.view = viewFiles
					files := listSkillFiles(s.dir)
					items := make([]list.Item, len(files))
					for i, f := range files {
						items[i] = fileItem{f}
					}
					delegate := list.NewDefaultDelegate()
					delegate.ShowDescription = false
					delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
						Foreground(lipgloss.Color("#7C3AED")).
						BorderLeftForeground(lipgloss.Color("#7C3AED"))
					m.fileList = list.New(items, delegate, m.width, m.height)
					m.fileList.Title = fmt.Sprintf("%s  files", s.name)
					m.fileList.SetShowStatusBar(true)
					m.fileList.SetFilteringEnabled(true)
					m.fileList.AdditionalShortHelpKeys = func() []key.Binding {
						return []key.Binding{
							key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "edit file")),
							key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc/q", "back")),
						}
					}
					return m, nil
				}

			case viewFiles:
				if item, ok := m.fileList.SelectedItem().(fileItem); ok {
					editor := editorCmd()
					c := exec.Command(editor, item.f.absPath)
					c.Stdin = os.Stdin
					c.Stdout = os.Stdout
					c.Stderr = os.Stderr
					return m, tea.ExecProcess(c, func(err error) tea.Msg {
						return editorFinishedMsg{err}
					})
				}
			}
		}
	}

	// Pass through to the active list
	var cmd tea.Cmd
	switch m.view {
	case viewSkills:
		m.skillList, cmd = m.skillList.Update(msg)
	case viewFiles:
		m.fileList, cmd = m.fileList.Update(msg)
	}
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	switch m.view {
	case viewFiles:
		return m.fileList.View()
	default:
		return m.skillList.View()
	}
}

// --- Main ---

func main() {
	skills, err := discoverSkills()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nMake sure the Claude Desktop app is installed and you have skills set up.\n")
		os.Exit(1)
	}
	if len(skills) == 0 {
		fmt.Fprintln(os.Stderr, "No skills found in the Claude Desktop app.")
		fmt.Fprintf(os.Stderr, "Expected location: %s\n", skillsBaseDir())
		os.Exit(1)
	}

	// Quick non-interactive list mode
	if len(os.Args) > 1 && os.Args[1] == "--list" {
		for _, s := range skills {
			fmt.Printf("%-20s [%s] %s\n", s.name, s.profile.label, s.description)
		}
		return
	}

	p := tea.NewProgram(initialModel(skills), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
