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

func cliSkillsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "skills")
}

func desktopSkillsBaseDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "Claude", "local-agent-mode-sessions", "skills-plugin")
	case "linux":
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

func discoverCLISkills() ([]skill, error) {
	base := cliSkillsDir()
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("cannot read skills directory %s: %w", base, err)
	}

	profile := skillProfile{label: "cli", uuid: "cli", skillsDir: base}
	var skills []skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(base, entry.Name())
		skillMD := filepath.Join(skillDir, "SKILL.md")
		if _, err := os.Stat(skillMD); err != nil {
			continue
		}
		name, desc := parseSkillFrontmatter(skillMD)
		if name == "" {
			name = entry.Name()
		}
		skills = append(skills, skill{
			name:        name,
			description: desc,
			dir:         skillDir,
			profile:     profile,
		})
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].name < skills[j].name
	})
	return skills, nil
}

func discoverDesktopSkills() ([]skill, error) {
	base := desktopSkillsBaseDir()
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

// openInFileManager opens a directory in the platform's file manager.
func openInFileManager(dir string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", dir).Start()
	case "linux":
		if _, err := exec.LookPath("xdg-open"); err == nil {
			return exec.Command("xdg-open", dir).Start()
		}
		// Try common file managers directly
		for _, fm := range []string{"nautilus", "dolphin", "thunar", "nemo", "pcmanfm"} {
			if _, err := exec.LookPath(fm); err == nil {
				return exec.Command(fm, dir).Start()
			}
		}
		return fmt.Errorf("no file manager found")
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// openInTerminalEditor uses $EDITOR/$VISUAL/nano — for CLI users who live in the terminal.
func openInTerminalEditor(path string) *exec.Cmd {
	if e := os.Getenv("EDITOR"); e != "" {
		return exec.Command(e, path)
	}
	if e := os.Getenv("VISUAL"); e != "" {
		return exec.Command(e, path)
	}
	return exec.Command("nano", path)
}

// openInGUIEditor tries GUI-friendly editors first, then falls back to $EDITOR/nano.
// Returns the exec.Cmd ready to run.
func openInGUIEditor(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		// "open -t" opens in the user's default plain-text editor (TextEdit usually)
		return exec.Command("open", "-t", "-W", path)
	case "linux":
		// Try xdg-open first (respects desktop environment defaults)
		if _, err := exec.LookPath("xdg-open"); err == nil {
			return exec.Command("xdg-open", path)
		}
		// Try common GUI editors
		for _, editor := range []string{"gedit", "kate", "mousepad", "xed", "pluma"} {
			if _, err := exec.LookPath(editor); err == nil {
				return exec.Command(editor, path)
			}
		}
	}

	// Fall back to $EDITOR / $VISUAL / nano
	if e := os.Getenv("EDITOR"); e != "" {
		return exec.Command(e, path)
	}
	if e := os.Getenv("VISUAL"); e != "" {
		return exec.Command(e, path)
	}
	return exec.Command("nano", path)
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
	printPath string // set when user presses 'p' — printed after TUI exits
	cliMode   bool   // true = ~/.claude/skills, terminal editor first
}

type editorFinishedMsg struct{ err error }

func initialModel(skills []skill, cliMode bool) model {
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

	var title string
	if cliMode {
		title = "Claude Code Skills (~/.claude/skills)"
	} else {
		title = "Claude Desktop Skills"
		if len(profiles) > 1 {
			title = fmt.Sprintf("Claude Desktop Skills (%d profiles)", len(profiles))
		}
	}

	sl := list.New(items, delegate, 80, 24)
	sl.Title = title
	sl.SetShowStatusBar(true)
	sl.SetFilteringEnabled(true)
	sl.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open skill")),
			key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open in finder")),
			key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "print path")),
		}
	}

	return model{
		view:      viewSkills,
		skillList: sl,
		skills:    skills,
		cliMode:   cliMode,
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

		case "p":
			switch m.view {
			case viewSkills:
				if item, ok := m.skillList.SelectedItem().(skillItem); ok {
					m.printPath = item.s.dir
					m.quitting = true
					return m, tea.Quit
				}
			case viewFiles:
				if item, ok := m.fileList.SelectedItem().(fileItem); ok {
					m.printPath = item.f.absPath
					m.quitting = true
					return m, tea.Quit
				}
			}

		case "o":
			// Open the skill folder in the system file manager
			var dir string
			switch m.view {
			case viewSkills:
				if item, ok := m.skillList.SelectedItem().(skillItem); ok {
					dir = item.s.dir
				}
			case viewFiles:
				if m.current != nil {
					dir = m.current.dir
				}
			}
			if dir != "" {
				openInFileManager(dir) // fire and forget — it's a GUI app
			}
			return m, nil

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
							key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open folder")),
							key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "print path")),
							key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc/q", "back")),
						}
					}
					return m, nil
				}

			case viewFiles:
				if item, ok := m.fileList.SelectedItem().(fileItem); ok {
					var c *exec.Cmd
					if m.cliMode {
						c = openInTerminalEditor(item.f.absPath)
					} else {
						c = openInGUIEditor(item.f.absPath)
					}
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
	// Parse flags: pull out --cli early so it combines with other flags
	cliMode := false
	args := []string{}
	for _, a := range os.Args[1:] {
		if a == "--cli" {
			cliMode = true
		} else {
			args = append(args, a)
		}
	}

	// Discover skills based on mode
	var skills []skill
	var err error
	var baseDir string
	if cliMode {
		baseDir = cliSkillsDir()
		skills, err = discoverCLISkills()
	} else {
		baseDir = desktopSkillsBaseDir()
		skills, err = discoverDesktopSkills()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(skills) == 0 {
		fmt.Fprintln(os.Stderr, "No skills found.")
		fmt.Fprintf(os.Stderr, "Expected location: %s\n", baseDir)
		os.Exit(1)
	}

	if len(args) > 0 {
		switch args[0] {
		case "--help", "-h":
			fmt.Println("skill-editor — browse and edit Claude skills")
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("  skill-editor              Browse Claude Desktop skills (GUI editor)")
			fmt.Println("  skill-editor --cli        Browse Claude Code skills ($EDITOR)")
			fmt.Println("  skill-editor --list       List all skills (non-interactive)")
			fmt.Println("  skill-editor --cli --list List CLI skills (non-interactive)")
			fmt.Println("  skill-editor --open       Open skills directory in file manager")
			fmt.Println("  skill-editor --help       Show this help")
			fmt.Println()
			fmt.Println("TUI keys:")
			fmt.Println("  enter   Open skill / edit file")
			fmt.Println("  o       Open skill folder in file manager")
			fmt.Println("  p       Print path and exit")
			fmt.Println("  /       Filter list")
			fmt.Println("  esc/q   Back / quit")
			return
		case "--list":
			for _, s := range skills {
				if cliMode {
					fmt.Printf("%-20s %s\n", s.name, s.description)
				} else {
					fmt.Printf("%-20s [%s] %s\n", s.name, s.profile.label, s.description)
				}
			}
			return
		case "--open":
			if err := openInFileManager(baseDir); err != nil {
				fmt.Fprintf(os.Stderr, "Could not open file manager: %v\n", err)
				fmt.Fprintf(os.Stderr, "Skills directory: %s\n", baseDir)
				os.Exit(1)
			}
			fmt.Printf("Opened %s\n", baseDir)
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\nTry: skill-editor --help\n", args[0])
			os.Exit(1)
		}
	}

	prog := tea.NewProgram(initialModel(skills, cliMode), tea.WithAltScreen())
	finalModel, err := prog.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	// If the user pressed 'p', the TUI is fully torn down now — safe to print
	if m, ok := finalModel.(model); ok && m.printPath != "" {
		fmt.Println(m.printPath)
	}
}
