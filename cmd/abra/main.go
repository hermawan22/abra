package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	internalversion "github.com/hermawan22/abra/internal/version"
)

const (
	checkoutEnvPath       = ".tmp/quickstart.env"
	defaultBaseURL        = "http://127.0.0.1:18080"
	defaultToken          = "dev-token"
	defaultHTTPTimeout    = 30 * time.Second
	defaultIngestTimeout  = 10 * time.Minute
	defaultWorkerInterval = 30 * time.Second
	maxCLIResponseBody    = 8 << 20
	installScript         = "https://github.com/hermawan22/abra/releases/latest/download/install.sh"

	directIngestBatchMaxDocuments    = 50
	directIngestBatchMaxPayloadBytes = 4 << 20
	directIngestBatchMaxChunks       = 100
	directIngestChunkEstimateChars   = 1080
)

var (
	version = internalversion.Version
	commit  = internalversion.Commit
	date    = internalversion.Date
)

type cliArgs struct {
	Command string
	Flags   map[string]string
	Bools   map[string]bool
	Rest    []string
}

type connectorManifest struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Scope               string            `json:"scope"`
	MCPURL              string            `json:"mcp_url"`
	ServerURL           string            `json:"server_url"`
	URL                 string            `json:"url"`
	Tool                string            `json:"tool"`
	Arguments           map[string]any    `json:"arguments"`
	ConnectorKind       string            `json:"connector_kind"`
	DocumentSourceType  string            `json:"document_source_type"`
	BearerTokenEnv      string            `json:"bearer_token_env"`
	HeaderEnv           map[string]string `json:"header_env"`
	Status              string            `json:"status"`
	Authority           string            `json:"authority"`
	AuthorityScore      *float64          `json:"authority_score"`
	FreshnessSeconds    int               `json:"freshness_seconds"`
	Schedule            string            `json:"schedule"`
	VerifyQuery         string            `json:"verify_query"`
	AllowPrivateNetwork bool              `json:"allow_private_network,omitempty"`
	Metadata            map[string]any    `json:"metadata"`
}

type directIngestBatchLimits struct {
	MaxDocuments    int
	MaxPayloadBytes int
	MaxChunks       int
}

type directIngestBatch struct {
	Start int
	End   int
}

var defaultDirectIngestBatchLimits = directIngestBatchLimits{
	MaxDocuments:    directIngestBatchMaxDocuments,
	MaxPayloadBytes: directIngestBatchMaxPayloadBytes,
	MaxChunks:       directIngestBatchMaxChunks,
}

type contextConfig struct {
	EnvFile string
	BaseURL string
	Token   string
}

func main() {
	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintln(os.Stderr, recovered)
			os.Exit(1)
		}
	}()
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type cliCommandHandler func(context.Context, cliArgs) error

var cliCommandHandlers = map[string]cliCommandHandler{
	"version":      func(_ context.Context, args cliArgs) error { return printVersion(args) },
	"--version":    func(_ context.Context, args cliArgs) error { return printVersion(args) },
	"-v":           func(_ context.Context, args cliArgs) error { return printVersion(args) },
	"install":      setup,
	"setup":        setup,
	"upgrade":      func(_ context.Context, args cliArgs) error { return upgrade(args) },
	"update":       func(_ context.Context, args cliArgs) error { return upgrade(args) },
	"uninstall":    func(_ context.Context, args cliArgs) error { return uninstall(args) },
	"demo":         demo,
	"quickstart":   demo,
	"init":         func(_ context.Context, args cliArgs) error { return initEnv(args) },
	"config":       func(_ context.Context, args cliArgs) error { return configCommand(args) },
	"model":        modelCommand,
	"models":       models,
	"scope":        func(_ context.Context, args cliArgs) error { return scopeCommand(args) },
	"agent":        agentCommand,
	"agents":       agentsCommand,
	"ui":           removedUICommand,
	"dashboard":    removedUICommand,
	"up":           up,
	"start":        up,
	"down":         func(_ context.Context, args cliArgs) error { return down(args) },
	"stop":         func(_ context.Context, args cliArgs) error { return down(args) },
	"status":       status,
	"doctor":       doctor,
	"seed":         seed,
	"connect":      connectCommand,
	"sync":         syncCommand,
	"ingest":       ingestCommand,
	"watch":        watch,
	"source":       watch,
	"connectors":   connectorsCommand,
	"connector":    connectorsCommand,
	"sources":      listSources,
	"jobs":         listJobs,
	"approvals":    approvalsCommand,
	"approval":     approvalsCommand,
	"observe":      observe,
	"observations": listObservations,
	"episodes":     listObservations,
	"ask":          think,
	"think":        think,
	"recall":       recall,
	"context":      composeMemory,
	"compose":      composeMemory,
	"brain":        brainCommand,
	"memory":       memoryCommand,
	"govern":       governCommand,
	"eval":         evalCommand,
	"plugin":       pluginCommand,
	"plugins":      pluginCommand,
	"mcp":          mcp,
	"mcp-config":   mcp,
}

func run(ctx context.Context, argv []string) error {
	args := parseArgs(argv)
	if args.Command != "" && boolFlag(args, "help") {
		fmt.Print(commandUsage(args.Command))
		return nil
	}
	if args.Command == "help" && len(args.Rest) > 0 {
		fmt.Print(commandUsage(args.Rest[0]))
		return nil
	}
	if args.Command == "" || args.Command == "help" || args.Command == "-h" || args.Command == "--help" {
		fmt.Print(usage())
		return nil
	}
	handler := cliCommandHandlers[args.Command]
	if handler == nil {
		return fmt.Errorf("unknown command %q\n\n%s", args.Command, usage())
	}
	return handler(ctx, args)
}

func removedUICommand(context.Context, cliArgs) error {
	return errors.New("abra ui was removed; use `abra setup` for guided onboarding or `abra up` for non-interactive start")
}

func modelCommand(ctx context.Context, args cliArgs) error {
	action := ""
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
	}
	switch action {
	case "", "show", "config":
		return configShow(args)
	case "local", "qwen3", "local-smart", "openai", "compatible", "openai-compatible":
		return configModel(args)
	case "up", "start", "status", "check", "logs", "log", "down", "stop":
		return models(ctx, args)
	default:
		return fmt.Errorf("unknown model command %q\n\n%s", action, commandUsage("model"))
	}
}

func agentCommand(ctx context.Context, args cliArgs) error {
	action := ""
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "bootstrap", "init", "verify", "check", "ready":
		args.Rest = append([]string{action}, args.Rest...)
		return agentsCommand(ctx, args)
	case "install", "connect":
		agent := "codex"
		if len(args.Rest) > 0 {
			agent = strings.ToLower(strings.TrimSpace(args.Rest[0]))
			args.Rest = args.Rest[1:]
		}
		if isCodexAgent(agent) {
			return installCodexMCP(ctx, args)
		}
		fmt.Println("Automatic agent install is currently Codex-only.")
		fmt.Println("Generic MCP config: abra mcp > .tmp/abra.mcp.json")
		fmt.Println("Endpoint: " + strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp")
		fmt.Println("Token env: ABRA_API_TOKEN")
		return nil
	case "status", "doctor":
		return mcpStatus(ctx, args)
	case "", "help":
		return errors.New(commandUsage("agent"))
	default:
		return fmt.Errorf("unknown agent command %q\n\n%s", action, commandUsage("agent"))
	}
}

func printVersion(args cliArgs) error {
	executable, _ := os.Executable()
	info := map[string]any{
		"version":    version,
		"commit":     commit,
		"date":       date,
		"goos":       runtime.GOOS,
		"goarch":     runtime.GOARCH,
		"executable": executable,
	}
	if boolFlag(args, "json") {
		return printJSON(info)
	}
	fmt.Println("abra " + version)
	fmt.Println("commit: " + commit)
	fmt.Println("date: " + date)
	fmt.Println("target: " + runtime.GOOS + "/" + runtime.GOARCH)
	return nil
}

func parseArgs(argv []string) cliArgs {
	args := cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}}
	if len(argv) > 0 {
		args.Command = argv[0]
		argv = argv[1:]
	}
	for i := 0; i < len(argv); i++ {
		item := argv[i]
		if !strings.HasPrefix(item, "--") {
			args.Rest = append(args.Rest, item)
			continue
		}
		key := strings.TrimPrefix(item, "--")
		if before, after, ok := strings.Cut(key, "="); ok {
			args.Flags[before] = after
			continue
		}
		if i+1 >= len(argv) || strings.HasPrefix(argv[i+1], "--") {
			args.Bools[key] = true
			continue
		}
		args.Flags[key] = argv[i+1]
		i++
	}
	return args
}

func copyCLIArgs(args cliArgs) cliArgs {
	out := cliArgs{
		Command: args.Command,
		Flags:   map[string]string{},
		Bools:   map[string]bool{},
		Rest:    append([]string(nil), args.Rest...),
	}
	for key, value := range args.Flags {
		out.Flags[key] = value
	}
	for key, value := range args.Bools {
		out.Bools[key] = value
	}
	return out
}

type rerankerCLIConfig struct {
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	Timeout  string
}

type localIngestPayloads struct {
	Documents    []map[string]any
	Paths        []string
	SourceURLs   []string
	SkippedEmpty int
}

type sourceIngestSpec struct {
	SourceType string
	SourceURL  string
	ScopeHint  string
	Config     map[string]any
}

type sourceIngestOutput struct {
	Scope       string
	SourceID    string
	SourceName  string
	SourceURL   string
	SourceType  string
	Status      string
	ShouldQueue bool
	Source      map[string]any
	Job         map[string]any
	JobID       string
}

type conversationTurn struct {
	Role    string
	Content string
	Index   int
}

type agentInstructionFile struct {
	Path    string
	Content string
}

type httpStatusError struct {
	Code    int
	Body    string
	Payload map[string]any
}

type envDefault struct {
	key   string
	value string
}

const demoEnvTemplate = `COMPOSE_FILE={{COMPOSE_FILE}}
ABRA_API_KEYS=demo-only-dev-token
ABRA_API_TOKEN=demo-only-dev-token
NODE_ENV=development
ABRA_APPROVAL_MODE=advisory
ABRA_PORT=18080
ABRA_IMAGE={{ABRA_IMAGE}}
POSTGRES_IMAGE=pgvector/pgvector:pg16
POSTGRES_USER=abra
POSTGRES_PASSWORD=abra
POSTGRES_DB=abra
POSTGRES_PORT=5433
ABRA_DATABASE_URL=postgres://abra:abra@postgres:5432/abra
EMBEDDING_PROVIDER=local
EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1
EMBEDDING_MODEL=Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0
EMBEDDING_DIMENSIONS=1024
EMBEDDING_TIMEOUT=10m
ABRA_AI_PROVIDER_CONCURRENCY=1
ABRA_EMBEDDING_BATCH_MAX_ITEMS=6
ABRA_EMBEDDING_BATCH_MAX_TOKENS=3000
RERANKER_PROVIDER=
RERANKER_BASE_URL=
RERANKER_MODEL=
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
ABRA_LOCAL_EMBEDDING_IMAGE=
ABRA_LOCAL_EMBEDDING_PULL_POLICY=missing
ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT=10s
REDACT_PII=true
RATE_LIMIT_MAX=1000
RATE_LIMIT_WINDOW=1m
ABRA_API_READ_TIMEOUT=10m
ABRA_MAX_REQUEST_BODY_BYTES=26214400
WORKER_INTERVAL=30s
WORKER_MAX_SOURCES_PER_RUN=25
WORKER_CONCURRENCY=1
ABRA_DEPLOYMENT_ENVIRONMENT=development
`

const productionEnvExample = `NODE_ENV=production
ABRA_API_KEYS=replace-with-generated-token
ABRA_WEBHOOK_SECRETS=replace-with-webhook-signing-secret
ABRA_APPROVAL_MODE=enforce
# Replace these placeholder digests with the release image digest and an operator-verified pgvector digest.
ABRA_IMAGE=ghcr.io/hermawan22/abra@sha256:0000000000000000000000000000000000000000000000000000000000000000
POSTGRES_IMAGE=pgvector/pgvector@sha256:0000000000000000000000000000000000000000000000000000000000000000
POSTGRES_USER=abra
POSTGRES_PASSWORD=replace-with-generated-database-password
POSTGRES_DB=abra
ABRA_DATABASE_URL=postgres://abra:replace-with-generated-database-password@postgres:5432/abra
EMBEDDING_PROVIDER=compatible
EMBEDDING_BASE_URL=https://embedding-provider.example/v1
EMBEDDING_API_KEY=replace-with-embedding-key
EMBEDDING_MODEL=embedding-model
EMBEDDING_DIMENSIONS=1024
EMBEDDING_TIMEOUT=30s
ABRA_AI_PROVIDER_CONCURRENCY=4
ABRA_EMBEDDING_BATCH_MAX_ITEMS=16
ABRA_EMBEDDING_BATCH_MAX_TOKENS=6000
RERANKER_PROVIDER=
RERANKER_BASE_URL=
RERANKER_API_KEY=
RERANKER_MODEL=
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
ABRA_LOCAL_EMBEDDING_IMAGE=
ABRA_LOCAL_EMBEDDING_PULL_POLICY=missing
ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT=10s
REDACT_PII=true
RATE_LIMIT_MAX=120
RATE_LIMIT_WINDOW=1m
ABRA_API_READ_TIMEOUT=2m
ABRA_MAX_REQUEST_BODY_BYTES=26214400
WORKER_INTERVAL=30s
WORKER_MAX_SOURCES_PER_RUN=25
WORKER_CONCURRENCY=1
ABRA_PORT=18080
`
