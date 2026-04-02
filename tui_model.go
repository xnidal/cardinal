package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cardinal/pkg/api"
	"cardinal/pkg/config"
	"cardinal/pkg/memory"
	"cardinal/pkg/storage"
	"cardinal/pkg/tools"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Model struct {
	input            textinput.Model
	spinner          spinner.Model
	messages         []api.Message
	streaming        string
	thinking         string
	pendingToolCalls []api.ToolCall
	streamCh         <-chan api.StreamEvent
	err              error
	client           *api.Client
	toolDefs         []api.Tool
	working          string
	width            int
	height           int
	cfg              *config.Config
	suggestions      []string
	suggSelected     int
	scrollOffset     int
	viewport         viewport.Model
	useViewport      bool
	mode             string
	modeData         interface{}
	busy             bool
	status           string
	soul             string
	retryCount       int
	lastMessages     []api.Message
	autoApprove      bool
	promptHistory    []string
	historyIndex     int
	thinkingIdx      int
	lastStatus       string
	contextUsed      int
	contextLimit     int
	memoryIntegration  *memory.MemoryIntegration
}

var slashCommands = []string{
	"/help",
	"/clear",
	"/undo",
	"/soul",
	"/models",
	"/profiles",
	"/profile new",
	"/profile edit",
	"/endpoint",
	"/apikey",
	"/tools",
	"/autoapprove",
}

func NewModel(cfg *config.Config) Model {
	ti := textinput.New()
	ti.Placeholder = "Message Cardinal or type /help"
	ti.Focus()
	ti.CharLimit = 4000
	ti.Width = 60

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	working, _ := os.Getwd()

	vp := viewport.New(80, 20)
	vp.SetContent("")

	// Initialize memory integration
	memIntegration, err := memory.NewIntegration()
	if err != nil {
		// Log but continue - memory is optional
		fmt.Fprintf(os.Stderr, "Warning: memory integration failed: %v\n", err)
	}

	return Model{
		input:        ti,
		spinner:      s,
		client:       api.NewClient(cfg.APIURL, cfg.APIKey),
		toolDefs:     convertToolDefs(tools.GetToolDefinitions()),
		working:      working,
		cfg:          cfg,
		status:       "Ready",
		soul:         loadSoul(),
		viewport:     vp,
		autoApprove:  false,
		contextLimit: 128000,
		memoryIntegration: memIntegration,
	}
}

func loadSoul() string {
	candidates := []string{storage.GetConfigDir()}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".cardinal"))
	}
	for _, dir := range candidates {
		soulPath := filepath.Join(dir, "SOUL.md")
		data, err := os.ReadFile(soulPath)
		if err == nil {
			if s := strings.TrimSpace(string(data)); s != "" {
				return s
			}
		}
	}
	return ""
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func startSpinnerTicker() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinner.TickMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.useViewport && m.mode == "" && m.viewport.YOffset >= 0 {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.Type {
			case tea.KeyUp:
				m.viewport.LineUp(3)
				return m, nil
			case tea.KeyDown:
				m.viewport.LineDown(3)
				return m, nil
			case tea.KeyCtrlU:
				m.viewport.HalfViewUp()
				return m, nil
			case tea.KeyCtrlD:
				m.viewport.HalfViewDown()
				return m, nil
			}
		case tea.MouseMsg:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case spinner.TickMsg:
		m.spinner, _ = m.spinner.Update(msg)
		if m.busy && m.status != "" && m.status != m.lastStatus {
			m.thinkingIdx++
			if m.thinkingIdx >= len(thinkingMessages) {
				m.thinkingIdx = 0
			}
			m.status = thinkingMessages[m.thinkingIdx]
		}
		m.lastStatus = m.status
		if m.busy {
			return m, startSpinnerTicker()
		}
		return m, nil

	case streamEventMsg:
		return m.handleStreamEvent(msg.event)

	case streamClosedMsg:
		return m.finishAssistantTurn()

	case streamRetryMsg:
		return m.handleRetry(msg)

	case toolExecutionMsg:
		return m.handleToolExecution(msg)

	case modelsFetchedMsg:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err
			m.status = "Unable to load models"
			return m, nil
		}
		active := 0
		for i, model := range msg.models {
			if model.ID == m.cfg.Model {
				active = i
				break
			}
		}
		m.mode = "models"
		filterInput := textinput.New()
		filterInput.Placeholder = "Filter models..."
		filterInput.Focus()
		filterInput.Width = max(m.width-10, 20)
		m.modeData = &modelsMode{models: msg.models, selected: active, filterInput: filterInput}
		m.resize(m.width, m.height)
		m.status = fmt.Sprintf("Loaded %d model%s", len(msg.models), pluralize(len(msg.models)))
		return m, nil

	case thinkingMsg:
		m.thinking = msg.thinking
		if m.useViewport {
			m.updateViewportContent()
		}
		return m, nil
	}

	if m.mode != "" {
		return m.updateMode(msg)
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		return m.handleMainKey(keyMsg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyEnter:
		value := strings.TrimSpace(m.input.Value())
		if value == "" {
			return m, nil
		}
		if strings.HasPrefix(value, "/") {
			return m.handleSlashCommand(value)
		}
		if m.busy {
			return m, nil
		}
		m.err = nil
		m.retryCount = 0
		m.messages = append(m.messages, api.Message{Role: "user", Content: value})
		m.lastMessages = append([]api.Message(nil), m.messages...)
		m.promptHistory = append(m.promptHistory, value)
		m.historyIndex = len(m.promptHistory)
		m.input.SetValue("")
		m.suggestions = nil
		m.scrollOffset = 0
		m.useViewport = false
		return m.beginStream()

	case tea.KeyTab:
		if len(m.suggestions) > 0 {
			m.input.SetValue(m.suggestions[m.suggSelected])
			m.input.CursorEnd()
			m.suggestions = nil
			return m, nil
		}
		if m.busy {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

	case tea.KeyUp:
		if len(m.suggestions) > 0 {
			if m.suggSelected > 0 {
				m.suggSelected--
			}
			return m, nil
		}
		if m.busy {
			return m, nil
		}
		if len(m.promptHistory) > 0 && m.historyIndex > 0 {
			m.historyIndex--
			m.input.SetValue(m.promptHistory[m.historyIndex])
			m.input.CursorEnd()
			return m, nil
		}
		if m.useViewport {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		if m.scrollOffset > 0 {
			m.scrollOffset--
		}
		return m, nil

	case tea.KeyDown:
		if len(m.suggestions) > 0 {
			if m.suggSelected < len(m.suggestions)-1 {
				m.suggSelected++
			}
			return m, nil
		}
		if m.busy {
			return m, nil
		}
		if len(m.promptHistory) > 0 && m.historyIndex < len(m.promptHistory)-1 {
			m.historyIndex++
			m.input.SetValue(m.promptHistory[m.historyIndex])
			m.input.CursorEnd()
			return m, nil
		}
		if len(m.promptHistory) > 0 && m.historyIndex == len(m.promptHistory)-1 {
			m.historyIndex++
			m.input.SetValue("")
			return m, nil
		}
		if m.useViewport {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		if m.scrollOffset < len(m.messages) {
			m.scrollOffset++
		}
		return m, nil

	case tea.KeyEscape:
		if len(m.suggestions) > 0 {
			m.suggestions = nil
			return m, nil
		}
		if m.busy {
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncSuggestions()
	return m, cmd
}

func (m *Model) syncSuggestions() {
	if !strings.HasPrefix(m.input.Value(), "/") {
		m.suggestions = nil
		m.suggSelected = 0
		return
	}
	input := strings.TrimSpace(m.input.Value())
	var next []string
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, input) {
			next = append(next, cmd)
		}
	}
	if slicesEqual(m.suggestions, next) {
		return
	}
	m.suggestions = next
	m.suggSelected = 0
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *Model) resize(width, height int) {
	m.width = width
	m.height = height
	m.input.Width = max(width-6, 20)

	// Calculate viewport height (total height minus header, input, footer, and padding)
	headerHeight := 4 // header + blank line
	inputHeight := 2  // input + newline
	footerHeight := 2 // footer hint
	viewportHeight := height - headerHeight - inputHeight - footerHeight
	if viewportHeight < 5 {
		viewportHeight = 5
	}
	m.viewport.Width = width
	m.viewport.Height = viewportHeight

	switch data := m.modeData.(type) {
	case *textInputMode:
		data.input.Width = max(width-8, 20)
	case *profileFormMode:
		for i := range data.inputs {
			data.inputs[i].Width = max(width-18, 20)
		}
	case *modelsMode:
		data.filterInput.Width = max(width-10, 20)
		data.visibleLines = max(height-8, 5)
	}

	// Re-render viewport content after resize
	if m.useViewport && m.width > 0 && m.height > 0 {
		m.updateViewportContent()
	}
}

func (m *Model) updateViewportContent() {
	content := m.renderConversationContent()
	m.viewport.SetContent(content)
}

func (m *Model) renderConversationContent() string {
	hasStreaming := strings.TrimSpace(m.streaming) != ""
	hasThinking := strings.TrimSpace(m.thinking) != ""

	if len(m.messages) == 0 && !hasStreaming && !hasThinking && m.err == nil {
		return m.renderWelcome()
	}

	var blocks []string

	if m.viewport.YOffset > 0 {
		blocks = append(blocks, dimStyle.Render(fmt.Sprintf(" ↑ %d lines scrolled", m.viewport.YOffset)))
	}

	for _, message := range m.messages {
		if rendered := m.renderMessage(message); rendered != "" {
			blocks = append(blocks, rendered, "")
		}
	}

	if hasStreaming || hasThinking {
		blocks = append(blocks, m.renderStreamingMessage())
	}

	if m.err != nil {
		blocks = append(blocks, errorStyle.Render(" Error: "+m.err.Error()))
	}

	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}
