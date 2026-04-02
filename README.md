# Cardinal

A terminal-based AI coding assistant with tool execution capabilities.

## Overview

Cardinal is a TUI (Terminal User Interface) application that provides an interactive chat interface with an AI assistant. It can execute various tools to help with coding tasks, including file operations, bash commands, and more.

## Features

- **Interactive TUI**: Built with Bubble Tea framework for a responsive terminal experience
- **Tool Execution**: Execute bash commands, read/write/edit files, search code, and more
- **Permission System**: Tools requiring modifications prompt for approval by default
- **Multiple Profiles**: Configure different API endpoints, models, and settings
- **Streaming Responses**: Real-time streaming of AI responses
- **Conversation History**: Navigate through previous prompts
- **SOUL.md Support**: Customize the assistant's personality in `~/.cardinal/SOUL.md`

## Installation

```bash
go build -o cardinal
```

## Usage

### Interactive Mode

```bash
./cardinal
```

### CLI Mode

```bash
./cardinal "list all go files in the project"
```

## Configuration

### Environment Variables

- `CARDINAL_API_URL` - API endpoint (default: `http://localhost:11434/v1`)
- `CARDINAL_API_KEY` - API key for authentication
- `CARDINAL_MODEL` - Model to use (default: `llama3.2`)
- `CARDINAL_SYSTEM_PROMPT` - Custom system prompt

### Profiles

- `/profiles` - List all profiles
- `/profile new` - Create a new profile
- `/profile edit` - Edit current profile
- `/profile <name>` - Switch to a profile

### SOUL.md

Place a `SOUL.md` file in `~/.cardinal/` to customize the assistant's personality:

```markdown
You are Cardinal.
```

## Dependencies

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI framework
- [Bubbles](https://github.com/charmbracelet/bubbles) - TUI components
- [Lipgloss](https://github.com/charmbracelet/lipgloss) - Styling

## License

MIT
