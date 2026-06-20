package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cardinal/pkg/api"
	"cardinal/pkg/config"
	"cardinal/pkg/storage"
	"cardinal/pkg/tools"
	"cardinal/pkg/ui"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var customSpinner = spinner.Spinner{
	Frames: []string{
		"⠋", // dots 1,2,3,5,6 - missing 4,7,8
		"⠙", // dots 1,2,3,5,7 - missing 4,6,8
		"⠹", // dots 1,2,3,5,8 - missing 4,6,7
		"⠸", // dots 1,2,3,6,8 - missing 4,5,7
		"⠼", // dots 1,2,4,5,6 - missing 3,7,8
		"⠴", // dots 1,2,4,5,7 - missing 3,6,8
		"⠦", // dots 1,2,4,5,8 - missing 3,6,7
		"⠧", // dots 1,2,4,6,7 - missing 3,5,8
		"⠇", // dots 1,2,4,6,8 - missing 3,5,7
		"⠏", // dots 1,2,4,7,8 - missing 3,5,6
	},
	FPS: 80,
}

type Model struct {
	input                 textarea.Model
	spinner               spinner.Model
	messages              []api.Message
	streaming             string
	thinking              string
	pendingToolCalls      []api.ToolCall
	streamCh              <-chan api.StreamEvent
	cancelFunc            context.CancelFunc
	err                   error
	client                *api.Client
	toolDefs              []api.Tool
	working               string
	width                 int
	height                int
	cfg                   *config.Config
	suggestions           []string
	suggSelected          int
	scrollOffset          int
	viewport              viewport.Model
	useViewport           bool
	mode                  string
	modeData              any
	busy                  bool
	status                string
	soul                  string
	retryCount            int
	lastMessages          []api.Message
	autoApprove           bool
	promptHistory         []string
	historyIndex          int
	thinkingIdx           int
	lastStatus            string
	contextUsed           int
	contextLimit          int
	streamChars           int
	thinkingChars         int
	toolCallChars         int
	toolCallName          string
	errorStatus           string
	errorStatusTime       time.Time
	showThinking          bool
	pendingScrollback     []string
	scrolledAssistantUpTo int
	completionChecks      int
	goalMode              bool
	goalText              string
	systemNotes           []string
	todoStore             *storage.TodoStore
	todoChecks            int // how many times we've asked the verifier about an idle todo list
	markdown              *ui.MarkdownRenderer
	startupGrace          time.Time // drop escape-sequence noise until this deadline
	pendingApproval       *permissionMode
}

func flushScrollbackCmd(lines []string) tea.Cmd {
	if len(lines) == 0 {
		return nil
	}
	return tea.Println(strings.Join(lines, "\n"))
}

var slashCommands = []string{
	"/help",
	"/clear",
	"/new",
	"/undo",
	"/soul",
	"/thinking",
	"/goal ",
	"/models",
	"/profiles",
	"/autoapprove",
}

func NewModel(cfg *config.Config) Model {
	ta := textarea.New()
	ta.Placeholder = "Ask Cardinal anything…  (/help for commands)"
	ta.Focus()
	ta.SetWidth(60)
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 4000

	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	ta.FocusedStyle.Text = lipgloss.NewStyle()
	ta.BlurredStyle = ta.FocusedStyle

	ta.KeyMap.InsertNewline = key.NewBinding()

	s := spinner.New()
	s.Spinner = customSpinner
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)

	working, _ := os.Getwd()

	vp := viewport.New(80, 20)
	vp.SetContent("")

	return Model{
		input:        ta,
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
	showThinking: false,
		todoStore:    storage.NewTodoStore(),
		markdown:     ui.NewMarkdownRenderer(80),
		startupGrace: time.Now().Add(2 * time.Second),
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
	return textarea.Blink
}

func startSpinnerTicker() tea.Cmd {
	return tea.Tick(125*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// scrollbackTickMsg handles slower UI updates like thinking message cycling.
// It runs at a lower frequency than the spinner to avoid excessive CPU.
func startScrollbackTicker() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return scrollbackTickMsg{}
	})
}

type spinnerTickMsg struct{}
type scrollbackTickMsg struct{}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// During startup, terminals like Ghostty reply to OSC 11 (background-colour)
	// queries and CSI cursor-position reports. These arrive as KeyMsg runes
	// that look like "]11;rgb:..." or digits from a cursor report. Drop any
	// such noise until the grace window expires.
	if !m.startupGrace.IsZero() && time.Now().Before(m.startupGrace) {
		if km, ok := msg.(tea.KeyMsg); ok {
			if isOSCLeak(km) {
				return m, nil
			}
		}
	}

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

	case spinnerTickMsg:
		m.spinner, _ = m.spinner.Update(spinner.TickMsg{})
		if m.busy {
			return m, startSpinnerTicker()
		}
		return m, nil

	case scrollbackTickMsg:
		if m.busy && m.status != "" {
			currentPriority := getStatusPriority(m.status)
			if currentPriority >= 3 && m.thinkingChars == 0 && m.streamChars == 0 && m.toolCallChars == 0 {
				m.thinkingIdx++
				if m.thinkingIdx >= len(thinkingMessages) {
					m.thinkingIdx = 0
				}
				m.status = thinkingMessages[m.thinkingIdx]
			}
		}
		m.lastStatus = m.status
		if m.busy {
			return m, startScrollbackTicker()
		}
		if len(m.pendingScrollback) > 0 {
			lines := m.pendingScrollback
			m.pendingScrollback = nil
			return m, flushScrollbackCmd(lines)
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

	case goalEvalMsg:
		return m.handleGoalEval(msg)

	case todoEvalMsg:
		return m.handleTodoEval(msg)

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
		visibleLines := max(m.height-8, 8)
		scroll := 0
		if active >= visibleLines {
			scroll = active - visibleLines/2
		}
		m.modeData = &modelsMode{models: msg.models, selected: active, scroll: scroll, visibleLines: visibleLines, filterInput: filterInput}
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
	// Inline tool-approval card is active: hijack keys here so users can
	// toggle approvals without typing into the prompt.
	if m.pendingApproval != nil {
		return m.applyInlineApproval(m.pendingApproval, msg)
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyEnter:
		if m.mode == "" && !m.busy {
			value := strings.TrimSpace(m.input.Value())
			if value == "" {
				return m, nil
			}
			if strings.HasPrefix(value, "/") {
				if len(m.suggestions) > 0 && m.suggSelected >= 0 && m.suggSelected < len(m.suggestions) {
					value = m.suggestions[m.suggSelected]
				}
				return m.handleSlashCommand(value)
			}
			m.err = nil
			m.retryCount = 0
			m.messages = append(m.messages, api.Message{Role: "user", Content: value})
			m.goalMode = false
			m.goalText = ""
			m.completionChecks = 0
			m.lastMessages = append([]api.Message(nil), m.messages...)
			m.promptHistory = append(m.promptHistory, value)
			m.historyIndex = len(m.promptHistory)
			m.input.SetValue("")
			m.suggestions = nil
			m.scrollOffset = 0
			m.useViewport = false
			return m.beginStream()
		}
		if m.mode == "" && m.busy && strings.HasPrefix(strings.TrimSpace(m.input.Value()), "/") {
			value := strings.TrimSpace(m.input.Value())
			if len(m.suggestions) > 0 && m.suggSelected >= 0 && m.suggSelected < len(m.suggestions) {
				value = m.suggestions[m.suggSelected]
			}
			return m.handleSlashCommand(value)
		}

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
			return m.cancelCurrentRequest()
		}

	case tea.KeyCtrlO:
		return m, nil

	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncSuggestions()
	return m, cmd
}

func (m Model) cancelCurrentRequest() (tea.Model, tea.Cmd) {
	if m.cancelFunc != nil {
		m.cancelFunc()
		m.cancelFunc = nil
	}

	streamingContent := strings.TrimSpace(m.streaming)
	thinkingContent := strings.TrimSpace(m.thinking)

	m.streamCh = nil
	m.streaming = ""
	m.thinking = ""
	m.pendingToolCalls = nil
	m.busy = false
	m.retryCount = 0

	if streamingContent != "" || thinkingContent != "" {
		m.addAssistantMessageWithThinking(streamingContent, thinkingContent)
		m.finalizeUIMessageHandling()
	}

	m.status = "Cancelled"
	m.errorStatus = "Request cancelled"
	m.errorStatusTime = time.Now()
	return m, nil
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
	m.input.SetWidth(max(width-6, 20))
	m.input.SetHeight(1)

	// Markdown body is narrower than the outer card: account for the
	// "● Title" header and the 2-char left padding of the message body.
	if m.markdown != nil {
		m.markdown.Resize(max(width-8, 24))
	}

	// Calculate viewport height (total height minus fixed elements)
	// Cardinal header: 1 line + 1 margin = 2
	// Empty line before input: 1
	// Input: 1 line
	// Footer: 1 line
	// Total fixed: 5
	headerHeight := 2
	spacingHeight := 1 // empty line before input
	inputHeight := 3   // textarea is taller than textinput
	footerHeight := 1
	viewportHeight := height - headerHeight - spacingHeight - inputHeight - footerHeight
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

type scrollbackMsg struct {
	lines []string
	kind  string
}

func scrollbackCmd(lines []string, kind string) tea.Cmd {
	if len(lines) == 0 {
		return nil
	}
	formatted := formatScrollback(lines, kind)
	return tea.Println(formatted)
}

func formatScrollback(lines []string, kind string) string {
	out := make([]string, 0, len(lines)+2)
	out = append(out, "─── "+kind+" ───")
	out = append(out, lines...)
	out = append(out, "────────────")
	return strings.Join(out, "\n")
}

func (m *Model) updateViewportContent() {
	content := m.renderConversationContent()
	m.viewport.SetContent(content)
}

func (m *Model) addSystemNote(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	m.systemNotes = append(m.systemNotes, text)
	if len(m.systemNotes) > 50 {
		m.systemNotes = m.systemNotes[len(m.systemNotes)-50:]
	}
	if m.useViewport {
		m.updateViewportContent()
	}
}

func (m *Model) renderConversationContent() string {
	return m.renderChatHistory(m.messages, 0, strings.TrimSpace(m.streaming) != "", strings.TrimSpace(m.thinking) != "", m.err)
}

// isOSCLeak detects KeyMsgs that are fragments of terminal escape-sequence
// replies (OSC 11 background-colour, CSI cursor-position, etc.) that leak
// into stdin on terminals like Ghostty. These arrive as short bursts of
// digits, semicolons, and the literal chars "rgb:". A normal user would
// never type these as the very first keys after launch.
func isOSCLeak(msg tea.KeyMsg) bool {
	if msg.Type != tea.KeyRunes {
		return false
	}
	s := msg.String()
	// Classic OSC 11 reply body: starts with "]" or contains "rgb:"
	if strings.HasPrefix(s, "]") || strings.Contains(s, "rgb:") {
		return true
	}
	// Cursor-position report digits like "77;1R" — short strings of
	// digits, semicolons, and an optional trailing letter (the CSI
	// final byte).
	if len(s) <= 10 && len(s) >= 2 {
		allDigitSemi := true
		hasDigit := false
		lastIdx := len(s) - 1
		for idx, r := range s {
			if r >= '0' && r <= '9' {
				hasDigit = true
			} else if r == ';' || r == ':' {
				// ok
			} else if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				// allow a single trailing letter (the CSI final byte)
				if idx != lastIdx {
					allDigitSemi = false
				}
			} else {
				allDigitSemi = false
			}
		}
		if allDigitSemi && hasDigit {
			return true
		}
	}
	return false
}
