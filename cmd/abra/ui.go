package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type uiItem struct {
	key         string
	title       string
	description string
}

type uiModel struct {
	ctx      context.Context
	args     cliArgs
	width    int
	height   int
	selected int
	screen   string
	busy     bool
	message  string
	errText  string
	lastKind string
	status   map[string]any
	config   map[string]string
	inputs   []textinput.Model
	focus    int
}

type uiResult struct {
	kind   string
	output string
	err    error
	status map[string]any
	config map[string]string
}

var uiItems = []uiItem{
	{key: "setup", title: "Setup", description: "Start or restart stack"},
	{key: "status", title: "Runtime", description: "Health and readiness"},
	{key: "config", title: "Model", description: "Connect embeddings"},
	{key: "ingest", title: "Local Ingest", description: "Index this repo"},
	{key: "git", title: "Remote Git", description: "Queue source ingest"},
	{key: "think", title: "Think", description: "Ask governed memory"},
	{key: "jobs", title: "Jobs", description: "Ingestion progress"},
	{key: "mcp", title: "MCP", description: "Client config"},
	{key: "doctor", title: "Doctor", description: "Run diagnostics"},
}

var (
	uiAccent       = lipgloss.Color("#7C5CFF")
	uiAccent2      = lipgloss.Color("#18A999")
	uiWarn         = lipgloss.Color("#F59E0B")
	uiError        = lipgloss.Color("#FF5C7A")
	uiMuted        = lipgloss.Color("#8A8FA3")
	uiPanelBorder  = lipgloss.Color("#30364A")
	uiTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F5F7FF"))
	uiMutedStyle   = lipgloss.NewStyle().Foreground(uiMuted)
	uiKeyStyle     = lipgloss.NewStyle().Foreground(uiAccent).Bold(true)
	uiPillStyle    = lipgloss.NewStyle().Padding(0, 1).Bold(true)
	uiPanelStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(uiPanelBorder).Padding(1, 2)
	uiActiveStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(uiAccent).Padding(1, 2)
	uiDangerStyle  = lipgloss.NewStyle().Foreground(uiError).Bold(true)
	uiSuccessStyle = lipgloss.NewStyle().Foreground(uiAccent2).Bold(true)
)

func runUI(ctx context.Context, args cliArgs) error {
	model := newUIModel(ctx, args)
	if boolFlag(args, "render") {
		if err := ensureEnvQuiet(args); err == nil {
			model.config, _ = readEnvValues(envPath(args))
		}
		status, _, err := getJSON(ctx, args, "/readyz")
		model.status = status
		if err != nil {
			model.message = "Runtime offline. Run: abra up"
		}
		fmt.Print(model.View())
		return nil
	}
	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func newUIModel(ctx context.Context, args cliArgs) uiModel {
	return uiModel{
		ctx:      ctx,
		args:     args,
		width:    100,
		height:   32,
		selected: 0,
		screen:   "home",
		message:  "Ready",
	}
}

func (m uiModel) Init() tea.Cmd {
	return uiRefreshCmd(m.ctx, m.args)
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case uiResult:
		m.busy = false
		m.errText = ""
		m.lastKind = msg.kind
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.message = "Action failed"
		} else if strings.TrimSpace(msg.output) != "" {
			m.message = strings.TrimSpace(msg.output)
		} else {
			m.message = "Done"
		}
		if msg.status != nil {
			m.status = msg.status
		}
		if msg.config != nil {
			m.config = msg.config
		}
		m.screen = "home"
		return m, nil
	case tea.KeyMsg:
		if m.busy && msg.String() != "ctrl+c" && msg.String() != "q" {
			return m, nil
		}
		if m.inForm() {
			return m.updateForm(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case "down", "j":
			if m.selected < len(uiItems)-1 {
				m.selected++
			}
			return m, nil
		case "r":
			m.busy = true
			m.message = "Refreshing runtime"
			return m, uiRefreshCmd(m.ctx, m.args)
		case "s":
			m.busy = true
			m.message = "Starting local stack"
			return m, uiRunCmd(m.ctx, m.args, []string{"up"})
		case "x":
			m.busy = true
			m.message = "Restarting local stack"
			return m, uiRunManyCmd(m.ctx, m.args, [][]string{{"down"}, {"up"}})
		case "l":
			m.busy = true
			m.message = "Switching to local embeddings"
			return m, uiRunCmd(m.ctx, m.args, []string{"config", "model", "local"})
		case "c":
			return m.openModelForm(), nil
		case "i":
			return m.openLocalIngestForm(), nil
		case "g":
			return m.openGitIngestForm(), nil
		case "t":
			return m.openThinkForm(), nil
		case "J":
			m.busy = true
			m.message = "Loading ingestion jobs"
			return m, uiRunCmd(m.ctx, m.args, []string{"jobs"})
		case "m":
			m.busy = true
			m.message = "Generating MCP config"
			return m, uiRunCmd(m.ctx, m.args, []string{"mcp"})
		case "d":
			m.busy = true
			m.message = "Running doctor"
			return m, uiRunCmd(m.ctx, m.args, []string{"doctor"})
		case "?":
			m.selected = len(uiItems) - 1
			return m, nil
		case "enter":
			return m.activateSelected()
		}
	}
	return m, nil
}

func (m uiModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = "home"
		m.message = "Canceled"
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "tab", "shift+tab", "up", "down":
		if msg.String() == "up" || msg.String() == "shift+tab" {
			m.focus--
		} else {
			m.focus++
		}
		if m.focus >= len(m.inputs) {
			m.focus = 0
		}
		if m.focus < 0 {
			m.focus = len(m.inputs) - 1
		}
		for i := range m.inputs {
			if i == m.focus {
				m.inputs[i].Focus()
			} else {
				m.inputs[i].Blur()
			}
		}
		return m, nil
	case "enter":
		if m.screen == "model-form" {
			baseURL := strings.TrimSpace(m.inputs[0].Value())
			apiKey := strings.TrimSpace(m.inputs[1].Value())
			model := strings.TrimSpace(m.inputs[2].Value())
			dims := strings.TrimSpace(m.inputs[3].Value())
			if apiKey == "" {
				apiKey = strings.TrimSpace(m.config["EMBEDDING_API_KEY"])
			}
			if baseURL == "" || apiKey == "" || model == "" || dims == "" {
				m.errText = "Base URL, API key, model, and dimensions are required"
				return m, nil
			}
			m.busy = true
			m.message = "Saving compatible model config"
			return m, uiRunCmd(m.ctx, m.args, []string{"config", "model", "compatible", "--base-url", baseURL, "--api-key", apiKey, "--model", model, "--dimensions", dims})
		}
		if m.screen == "local-ingest-form" {
			path := strings.TrimSpace(m.inputs[0].Value())
			scope := strings.TrimSpace(m.inputs[1].Value())
			include := strings.TrimSpace(m.inputs[2].Value())
			code := strings.TrimSpace(m.inputs[3].Value())
			if path == "" {
				m.errText = "Path is required"
				return m, nil
			}
			argv := []string{"ingest", path}
			if scope != "" {
				argv = append(argv, "--scope", scope)
			}
			if include != "" {
				argv = append(argv, "--include", include)
			}
			if yesish(code) {
				argv = append(argv, "--code")
			}
			m.busy = true
			m.message = "Ingesting local source"
			return m, uiRunCmd(m.ctx, m.args, argv)
		}
		if m.screen == "git-ingest-form" {
			repo := strings.TrimSpace(m.inputs[0].Value())
			ref := strings.TrimSpace(m.inputs[1].Value())
			scope := strings.TrimSpace(m.inputs[2].Value())
			if repo == "" {
				m.errText = "Repository URL is required"
				return m, nil
			}
			argv := []string{"ingest", "--git", repo}
			if ref != "" {
				argv = append(argv, "--ref", ref)
			}
			if scope != "" {
				argv = append(argv, "--scope", scope)
			}
			argv = append(argv, "--code", "--wait")
			m.busy = true
			m.message = "Queueing remote Git ingest"
			return m, uiRunCmd(m.ctx, m.args, argv)
		}
		question := strings.TrimSpace(m.inputs[0].Value())
		if question == "" {
			m.errText = "Question is required"
			return m, nil
		}
		m.busy = true
		m.message = "Thinking with source-backed memory"
		return m, uiRunCmd(m.ctx, m.args, []string{"think", question})
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m uiModel) activateSelected() (tea.Model, tea.Cmd) {
	switch uiItems[m.selected].key {
	case "setup":
		m.busy = true
		m.message = "Starting local stack"
		return m, uiRunCmd(m.ctx, m.args, []string{"up"})
	case "status":
		m.busy = true
		m.message = "Refreshing runtime"
		return m, uiRefreshCmd(m.ctx, m.args)
	case "config":
		return m.openModelForm(), nil
	case "ingest":
		return m.openLocalIngestForm(), nil
	case "git":
		return m.openGitIngestForm(), nil
	case "think":
		return m.openThinkForm(), nil
	case "jobs":
		m.busy = true
		m.message = "Loading ingestion jobs"
		return m, uiRunCmd(m.ctx, m.args, []string{"jobs"})
	case "mcp":
		m.busy = true
		m.message = "Generating MCP config"
		return m, uiRunCmd(m.ctx, m.args, []string{"mcp"})
	case "doctor":
		m.busy = true
		m.message = "Running doctor"
		return m, uiRunCmd(m.ctx, m.args, []string{"doctor"})
	default:
		m.message = "Use the shortcuts below"
		return m, nil
	}
}

func (m uiModel) openModelForm() uiModel {
	base := textinput.New()
	base.Placeholder = "https://api.openai.com/v1"
	base.Prompt = "Base URL  "
	base.SetValue(firstNonEmpty(m.config["EMBEDDING_BASE_URL"], "https://api.openai.com/v1"))
	base.Focus()

	key := textinput.New()
	key.Placeholder = "API key"
	if strings.TrimSpace(m.config["EMBEDDING_API_KEY"]) != "" {
		key.Placeholder = "saved key kept if blank"
	}
	key.Prompt = "API key   "
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '*'

	model := textinput.New()
	model.Placeholder = "text-embedding-3-small"
	model.Prompt = "Model     "
	model.SetValue(firstNonEmpty(m.config["EMBEDDING_MODEL"], "text-embedding-3-small"))

	dims := textinput.New()
	dims.Placeholder = "1536"
	dims.Prompt = "Dims      "
	dims.SetValue(firstNonEmpty(m.config["EMBEDDING_DIMENSIONS"], "1536"))

	m.inputs = []textinput.Model{base, key, model, dims}
	m.focus = 0
	m.screen = "model-form"
	m.errText = ""
	return m
}

func (m uiModel) openLocalIngestForm() uiModel {
	path := textinput.New()
	path.Placeholder = "."
	path.Prompt = "Path      "
	path.SetValue(".")
	path.Focus()

	scope := textinput.New()
	scope.Placeholder = scopeOrDefault(m.args, ".")
	scope.Prompt = "Scope     "
	scope.SetValue(flag(m.args, "scope", ""))

	include := textinput.New()
	include.Placeholder = "**/*.md"
	include.Prompt = "Include   "
	include.SetValue("**/*.md")

	code := textinput.New()
	code.Placeholder = "yes"
	code.Prompt = "Code      "
	code.SetValue("yes")

	m.inputs = []textinput.Model{path, scope, include, code}
	m.focus = 0
	m.screen = "local-ingest-form"
	m.errText = ""
	return m
}

func (m uiModel) openGitIngestForm() uiModel {
	repo := textinput.New()
	repo.Placeholder = "https://github.com/owner/repo.git"
	repo.Prompt = "Repo URL  "
	repo.Focus()

	ref := textinput.New()
	ref.Placeholder = "main"
	ref.Prompt = "Ref       "
	ref.SetValue("main")

	scope := textinput.New()
	scope.Placeholder = scopeOrDefault(m.args, ".")
	scope.Prompt = "Scope     "
	scope.SetValue(flag(m.args, "scope", ""))

	m.inputs = []textinput.Model{repo, ref, scope}
	m.focus = 0
	m.screen = "git-ingest-form"
	m.errText = ""
	return m
}

func (m uiModel) openThinkForm() uiModel {
	question := textinput.New()
	question.Placeholder = "What should I know before changing this project?"
	question.Prompt = "Question  "
	question.Focus()
	m.inputs = []textinput.Model{question}
	m.focus = 0
	m.screen = "think-form"
	m.errText = ""
	return m
}

func (m uiModel) View() string {
	if m.screen == "model-form" {
		return m.formView("Connect model", "Set an OpenAI-compatible embedding endpoint. Leave API key blank to keep the saved key.")
	}
	if m.screen == "local-ingest-form" {
		return m.formView("Ingest local source", "Index docs and optional code intelligence from a local path. Blank scope uses repo default.")
	}
	if m.screen == "git-ingest-form" {
		return m.formView("Ingest remote Git", "Queue a worker refresh from a provider-neutral Git URL. Code intelligence is enabled.")
	}
	if m.screen == "think-form" {
		return m.formView("Ask Abra", "Ask a source-backed question for the current repo scope. Enter runs think, Esc cancels.")
	}
	return m.homeView()
}

func (m uiModel) inForm() bool {
	switch m.screen {
	case "model-form", "local-ingest-form", "git-ingest-form", "think-form":
		return true
	default:
		return false
	}
}

func (m uiModel) homeView() string {
	leftWidth := 32
	rightWidth := maxInt(50, m.width-leftWidth-7)
	header := m.headerView()
	nav := m.navView(leftWidth)
	main := m.mainView(rightWidth)
	body := lipgloss.JoinHorizontal(lipgloss.Top, nav, "  ", main)
	footer := uiMutedStyle.Render("Keys: up/down select  enter open  s start  x restart  c model  l local  i ingest  g git  t think  J jobs  m mcp  d doctor  q quit")
	return strings.Join([]string{header, body, footer}, "\n\n")
}

func (m uiModel) headerView() string {
	status := "offline"
	statusColor := uiError
	if m.ready() {
		status = "ready"
		statusColor = uiAccent2
	}
	if m.busy {
		status = "working"
		statusColor = uiWarn
	}
	pill := uiPillStyle.Background(statusColor).Foreground(lipgloss.Color("#10121A")).Render(status)
	title := lipgloss.NewStyle().Bold(true).Foreground(uiAccent).Render("ABRA")
	sub := uiMutedStyle.Render("CLI brain cockpit")
	return lipgloss.JoinHorizontal(lipgloss.Center, title, "  ", pill, "  ", sub)
}

func (m uiModel) navView(width int) string {
	lines := []string{}
	for i, item := range uiItems {
		prefix := "  "
		style := lipgloss.NewStyle().Padding(0, 1).Width(width - 4)
		if i == m.selected {
			prefix = "> "
			style = style.Foreground(lipgloss.Color("#FFFFFF")).Background(uiAccent).Bold(true)
		}
		lines = append(lines, prefix+style.Render(item.title))
		lines = append(lines, "  "+uiMutedStyle.Width(width-2).Render(item.description))
	}
	return uiPanelStyle.Width(width).Render(strings.Join(lines, "\n"))
}

func (m uiModel) mainView(width int) string {
	if width < 50 {
		width = 50
	}
	sections := []string{
		m.focusCard(width),
		m.messageCard(width),
	}
	return uiActiveStyle.Width(width).Render(strings.Join(sections, "\n\n"))
}

func (m uiModel) focusCard(width int) string {
	switch uiItems[m.selected].key {
	case "setup":
		return m.setupCard(width)
	case "status":
		return m.runtimeCard(width)
	case "config":
		return m.configCard(width)
	case "ingest":
		return m.localIngestCard(width)
	case "git":
		return m.gitIngestCard(width)
	case "think":
		return m.thinkCard(width)
	case "jobs":
		return m.jobsCard(width)
	case "mcp":
		return m.mcpCard(width)
	case "doctor":
		return m.doctorCard(width)
	default:
		return m.runtimeCard(width)
	}
}

func (m uiModel) setupCard(width int) string {
	lines := []string{
		uiTitleStyle.Render("Setup"),
		row("Env file", envPath(m.args)),
		row("Runtime", boolText(m.ready(), "ready", "not running")),
		"",
		"Start the local stack from here, then connect a model or use local embeddings.",
		"",
		uiKeyStyle.Render("s") + " start stack   " + uiKeyStyle.Render("x") + " restart stack   " + uiKeyStyle.Render("d") + " doctor",
	}
	return lipgloss.NewStyle().Width(width - 6).Render(strings.Join(lines, "\n"))
}

func (m uiModel) runtimeCard(width int) string {
	embedding := stringValue(m.status["embedding_provider"], "unknown")
	auth := "-"
	if value, ok := m.status["auth_required"].(bool); ok {
		auth = fmt.Sprintf("%v", value)
	}
	approval := stringValue(m.status["approval_mode"], m.config["ABRA_APPROVAL_MODE"])
	lines := []string{
		uiTitleStyle.Render("Runtime"),
		row("Status", boolText(m.ready(), "ready", "not ready")),
		row("Base URL", cfg(m.args).BaseURL),
		row("Embedding", embedding),
		row("Auth", auth),
		row("Approval", approval),
		"",
		uiKeyStyle.Render("r") + " refresh   " + uiKeyStyle.Render("s") + " start   " + uiKeyStyle.Render("d") + " doctor",
	}
	return lipgloss.NewStyle().Width(width - 6).Render(strings.Join(lines, "\n"))
}

func (m uiModel) configCard(width int) string {
	provider := firstNonEmpty(m.config["EMBEDDING_PROVIDER"], "local")
	model := firstNonEmpty(m.config["EMBEDDING_MODEL"], "embedding-model-1536")
	dims := firstNonEmpty(m.config["EMBEDDING_DIMENSIONS"], "1536")
	base := m.config["EMBEDDING_BASE_URL"]
	if base == "" {
		base = "-"
	}
	lines := []string{
		uiTitleStyle.Render("Model"),
		row("Provider", provider),
		row("Model", model),
		row("Dimensions", dims),
		row("Base URL", base),
		row("API key", maskSecret(m.config["EMBEDDING_API_KEY"])),
		"",
		"Use local embeddings for offline dev, or connect any OpenAI-compatible embedding endpoint.",
		"",
		uiKeyStyle.Render("c") + " connect compatible   " + uiKeyStyle.Render("l") + " use local",
	}
	return lipgloss.NewStyle().Width(width - 6).Render(strings.Join(lines, "\n"))
}

func (m uiModel) localIngestCard(width int) string {
	scope := flag(m.args, "scope", "")
	if scope == "" {
		scope = scopeOrDefault(m.args, ".")
	}
	lines := []string{
		uiTitleStyle.Render("Local Ingest"),
		row("Path", "."),
		row("Scope", scope),
		row("Code", "enabled"),
		"",
		"Indexes markdown plus deterministic code intelligence. Code creates graph context and summaries, not trusted prose claims.",
		"",
		uiKeyStyle.Render("i") + " configure and ingest",
	}
	return lipgloss.NewStyle().Width(width - 6).Render(strings.Join(lines, "\n"))
}

func (m uiModel) gitIngestCard(width int) string {
	lines := []string{
		uiTitleStyle.Render("Remote Git"),
		"Queue a worker-managed source config for a Git repository.",
		"",
		"Use this when the source should refresh through Abra's worker instead of one manual local ingest.",
		"",
		uiKeyStyle.Render("g") + " configure remote Git ingest   " + uiKeyStyle.Render("J") + " view jobs",
	}
	return lipgloss.NewStyle().Width(width - 6).Render(strings.Join(lines, "\n"))
}

func (m uiModel) thinkCard(width int) string {
	scope := flag(m.args, "scope", "")
	if scope == "" {
		scope = scopeOrDefault(m.args, ".")
	}
	lines := []string{
		uiTitleStyle.Render("Think"),
		row("Scope", scope),
		row("Answer", "citations + decision gate"),
		"",
		"Ask what an agent should know before changing this project. Abra returns source-backed context, gaps, health, and proceed/review guidance.",
		"",
		uiKeyStyle.Render("t") + " ask a question",
	}
	return lipgloss.NewStyle().Width(width - 6).Render(strings.Join(lines, "\n"))
}

func (m uiModel) jobsCard(width int) string {
	lines := []string{
		uiTitleStyle.Render("Jobs"),
		"Inspect worker ingestion jobs for the current scope.",
		"",
		uiKeyStyle.Render("J") + " refresh jobs   " + uiKeyStyle.Render("g") + " queue remote Git",
	}
	return lipgloss.NewStyle().Width(width - 6).Render(strings.Join(lines, "\n"))
}

func (m uiModel) mcpCard(width int) string {
	lines := []string{
		uiTitleStyle.Render("MCP"),
		"Generate MCP client config for AI tools that can call Abra as a governed brain.",
		"",
		"Command: abra mcp > .tmp/abra.mcp.json",
		"",
		uiKeyStyle.Render("m") + " print MCP config",
	}
	return lipgloss.NewStyle().Width(width - 6).Render(strings.Join(lines, "\n"))
}

func (m uiModel) doctorCard(width int) string {
	lines := []string{
		uiTitleStyle.Render("Doctor"),
		"Run local diagnostics: Docker, env file, readiness, MCP, and browser UI removal check.",
		"",
		uiKeyStyle.Render("d") + " run doctor   " + uiKeyStyle.Render("r") + " refresh runtime",
	}
	return lipgloss.NewStyle().Width(width - 6).Render(strings.Join(lines, "\n"))
}

func (m uiModel) messageCard(width int) string {
	title := uiTitleStyle.Render("Output")
	message := strings.TrimSpace(m.message)
	if message == "" {
		message = "No output yet."
	}
	if m.errText != "" {
		message = uiDangerStyle.Render(m.errText)
	}
	limit := 14
	if strings.Contains(m.lastKind, "think") || strings.Contains(m.lastKind, "mcp") || strings.Contains(m.lastKind, "doctor") {
		limit = 22
	}
	return lipgloss.NewStyle().Width(width - 6).Render(title + "\n" + clampLines(message, limit))
}

func (m uiModel) formView(title, subtitle string) string {
	width := maxInt(70, minInt(m.width-6, 96))
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(uiAccent).Render("ABRA") + "  " + uiTitleStyle.Render(title),
		uiMutedStyle.Render(subtitle),
		"",
	}
	for _, input := range m.inputs {
		lines = append(lines, input.View())
	}
	if m.errText != "" {
		lines = append(lines, "", uiDangerStyle.Render(m.errText))
	}
	lines = append(lines, "", uiMutedStyle.Render("Keys: tab focus  enter submit  esc cancel  ctrl+c quit"))
	return uiActiveStyle.Width(width).Render(strings.Join(lines, "\n"))
}

func (m uiModel) ready() bool {
	ok, _ := m.status["ok"].(bool)
	return ok
}

func row(label, value string) string {
	if strings.TrimSpace(value) == "" {
		value = "-"
	}
	return uiMutedStyle.Width(14).Render(label) + value
}

func boolText(ok bool, yes, no string) string {
	if ok {
		return uiSuccessStyle.Render(yes)
	}
	return uiDangerStyle.Render(no)
}

func uiRefreshCmd(ctx context.Context, args cliArgs) tea.Cmd {
	return func() tea.Msg {
		status, _, err := getJSON(ctx, args, "/readyz")
		values := map[string]string{}
		if ensureErr := ensureEnvQuiet(args); ensureErr == nil {
			values, _ = readEnvValues(envPath(args))
		}
		if err != nil {
			return uiResult{kind: "refresh", output: "Runtime offline. Run: abra up", config: values}
		}
		return uiResult{kind: "refresh", status: status, config: values, output: "Runtime refreshed"}
	}
}

func uiRunCmd(ctx context.Context, args cliArgs, argv []string) tea.Cmd {
	return func() tea.Msg {
		output, err := captureRun(ctx, uiChildArgs(args, argv))
		values := map[string]string{}
		if ensureErr := ensureEnvQuiet(args); ensureErr == nil {
			values, _ = readEnvValues(envPath(args))
		}
		return uiResult{kind: strings.Join(argv, " "), output: output, err: err, config: values}
	}
}

func uiRunManyCmd(ctx context.Context, args cliArgs, commands [][]string) tea.Cmd {
	return func() tea.Msg {
		var outputs []string
		for _, argv := range commands {
			output, err := captureRun(ctx, uiChildArgs(args, argv))
			if strings.TrimSpace(output) != "" {
				outputs = append(outputs, strings.TrimSpace(output))
			}
			if err != nil {
				return uiResult{kind: "multi", output: strings.Join(outputs, "\n\n"), err: err}
			}
		}
		values := map[string]string{}
		if ensureErr := ensureEnvQuiet(args); ensureErr == nil {
			values, _ = readEnvValues(envPath(args))
		}
		status, _, _ := getJSON(ctx, args, "/readyz")
		return uiResult{kind: "multi", output: strings.Join(outputs, "\n\n"), status: status, config: values}
	}
}

func ensureEnvQuiet(args cliArgs) error {
	path := envPath(args)
	if fileExists(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := demoEnv
	if boolFlag(args, "production") {
		content = productionEnvExample
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func uiChildArgs(args cliArgs, argv []string) []string {
	out := append([]string{}, argv...)
	for _, name := range []string{"env-file", "env", "base-url", "token", "scope"} {
		value := flag(args, name, "")
		if value != "" && !hasArgFlag(out, name) {
			out = append(out, "--"+name, value)
		}
	}
	return out
}

func hasArgFlag(argv []string, name string) bool {
	long := "--" + name
	prefix := long + "="
	for _, item := range argv {
		if item == long || strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func yesish(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "1", "y", "yes", "true", "on", "code":
		return true
	default:
		return false
	}
}

func captureRun(ctx context.Context, argv []string) (string, error) {
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = writer
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		done <- buf.String()
	}()
	runErr := run(ctx, argv)
	_ = writer.Close()
	os.Stdout = oldStdout
	output := <-done
	_ = reader.Close()
	return output, runErr
}

func clampLines(value string, limit int) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) <= limit {
		return strings.Join(lines, "\n")
	}
	return strings.Join(append(lines[:limit], "..."), "\n")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
