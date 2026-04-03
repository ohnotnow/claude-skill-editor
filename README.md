# claude-skill-editor

A TUI for browsing and editing Claude skills — Desktop app and Claude Code CLI.

## Why this exists

The Claude Desktop app lets you install skills but gives you no way to edit them afterwards. Want to fix a typo? Re-upload a zip. Every time. The files are buried under two levels of UUID directories, so good luck finding them by hand.

This tool finds your installed skills, lists them, and lets you pick one to edit. It handles both the Desktop app's UUID maze and the CLI's simpler `~/.claude/skills/` folder.

## Getting started

Grab a pre-built binary from the [releases page](https://github.com/ohnotnow/claude-skill-editor/releases), `chmod +x` it, and put it somewhere on your PATH. Binaries are available for macOS (Intel and Apple Silicon), Linux (amd64 and arm64), and Windows.

Or build from source if you have Go 1.24+:

```bash
git clone https://github.com/ohnotnow/claude-skill-editor.git
cd claude-skill-editor
go build -o skill-editor .
```

## Usage

Run it with no arguments to browse Desktop app skills. It opens files in a GUI editor (TextEdit on macOS, xdg-open/gedit on Linux).

```bash
skill-editor
```

Use `--cli` to browse Claude Code skills from `~/.claude/skills/` instead. This uses your `$EDITOR` (falls back to nano).

```bash
skill-editor --cli
```

Other options:

```
skill-editor --list         List Desktop skills
skill-editor --cli --list   List CLI skills
skill-editor --open         Open skills directory in your file manager
skill-editor --help         Show all options
```

### TUI controls

| Key | Action |
|-----|--------|
| enter | Open a skill / edit a file |
| o | Open the skill folder in your file manager |
| p | Print the full path and exit (useful for scripting) |
| / | Filter the list |
| esc, q | Go back / quit |

The `p` key is handy when you just want the path. The TUI tears down cleanly before printing, so you can pipe or copy the output.

## Where skills actually live

Desktop app skills are buried under a platform-specific path with two levels of UUID subdirectories:

- macOS: `~/Library/Application Support/Claude/local-agent-mode-sessions/skills-plugin/<uuid>/<uuid>/skills/`
- Linux: `~/.config/Claude/local-agent-mode-sessions/skills-plugin/<uuid>/<uuid>/skills/`

The tool walks these for you. If you have multiple profiles (UUID sets), it shows a short prefix tag so you can tell them apart.

CLI skills are simpler: `~/.claude/skills/<skill-name>/SKILL.md`. No UUID nesting.

## Licence

MIT. See [LICENSE](LICENSE).
