package main

func usage() string {
	return `Abra agent brain operator CLI

Usage:
  abra setup
  abra eval brain --file brain-eval.json
  abra agent bootstrap --agent <agent>
  abra agent verify . --scope repo:project --agent <agent>
  abra connect local . --code --scope repo:project
  abra sync . --code --scope repo:project
  abra brain status --scope repo:project
  abra model
  abra model local
  abra model compatible --base-url <url> --model <model> --dimensions <n>
  abra plugin list
  abra doctor

System:
  abra status
  abra scope
  abra model status
  abra model up
  abra model logs
  abra govern status
  abra brain status
  abra plugin contract
  abra down [--reset] [--keep-models]
  abra upgrade [--version vX.Y.Z]
  abra uninstall --yes

Common flags:
  --base-url http://127.0.0.1:18080
  --env-file <path>
  --token <token>
  --json

First run:
  abra setup
  abra doctor
  cd /path/to/project
  abra scope
  abra agent bootstrap --agent <agent>
  fully restart the agent runtime
  abra agent verify . --scope <scope-from-abra-scope> --json
  agents use MCP working_memory_compose / brain_think with the verified scope

Abra is MCP-first for agents. CLI is for install, setup, source sync, operator
inspection, maintenance, and eval. HTTP is internal service transport; no
browser UI is shipped.
Low-level compatibility commands remain for existing automation, but are not
part of the product surface.
`
}

func commandUsage(command string) string {
	switch command {
	case "advanced":
		return `Advanced compatibility commands are intentionally hidden from the
default product surface.

Stable operator surface:
  abra setup
  abra doctor
  abra scope
  abra agent
  abra connect
  abra sync
  abra model
  abra brain
  abra govern
  abra plugin
  abra eval brain --file <file>

Agents should use MCP tools directly. Low-level legacy commands such as ask,
context, think, recall, compose, ingest, sources, jobs, approvals, observations,
memory, and mcp remain available for existing automation and focused debugging,
but new UX should not depend on them.
`
	case "connect":
		return `Usage:
  abra connect local . --code --scope repo:project [--no-wait]
  abra connect git https://github.com/owner/repo.git --code --scope repo:project [--ref main]
  abra connect mcp https://mcp.example.com/mcp --tool export_documents --scope team:docs [--schedule "@every 10m"]
  abra connect webhook sample --scope team:docs --connector knowledge-base
  abra connect list --scope repo:project
  abra connect status <source-config-id>
  abra connect logs <source-config-id>
  abra connect pause <source-config-id>
  abra connect resume <source-config-id> [--approval-id approval...]

Connect registers durable sources and queues the first sync. Local sources need
the worker to see the same filesystem path; use abra sync . --code for a
one-shot local ingest from the current terminal.
`
	case "sync":
		return `Usage:
  abra sync . --code --scope repo:project [--continue-on-error] [--quiet]
  abra sync ./notes.md --scope repo:project
  abra sync <source-config-id> [--wait]
  abra sync git https://github.com/owner/repo.git --code --scope repo:project [--ref main]
  abra sync status [<source-config-id>]
  abra sync logs <source-config-id>
  abra sync jobs --scope repo:project

Sync refreshes memory. A local path is ingested directly from the CLI process;
a source id queues an existing source config through the worker.
`
	case "ask":
		return `Usage:
  abra ask "question" --scope repo:demo [--agent codex] [--entity Name] [--as-of YYYY-MM-DD] [--include-historical] [--mode fast|balanced|deep] [--limit n] [--max-queries n] [--token-budget n] [--include-unverified] [--brief] [--agent-output] [--synthesize] [--json]

Asks the governed brain layer. Returns a cited answer, verification, gaps,
memory health, graph context, and an agent decision gate. Use --brief for a
compact answer with top evidence and trust status, or --agent-output for a
handoff-shaped answer. Use --synthesize only when the server has optional
synthesis enabled; deterministic source-backed output remains the default.
Use --mode fast to cap fanout, balanced for the default governed packet, or
deep for wider evidence without enabling synthesis.
`
	case "context":
		return `Usage:
  abra context "task" --scope repo:demo [--agent codex] [--entity Name] [--file path] [--changed-file path] [--language go] [--as-of YYYY-MM-DD] [--include-historical] [--mode fast|balanced|deep] [--limit n] [--max-queries n] [--token-budget n] [--include-unverified] [--hook before_task] [--brief] [--agent-output] [--prompt] [--persist-learning] [--json]

Builds task-specific working memory for AI agents. Human output includes the
decision gate, retrieval quality, health signals, validation plan, allowed next
actions, and suggested steps. Use --mode fast to cap fanout, balanced for the
default packet, or deep for wider evidence. Use --brief for a compact operator
view or --agent-output for a handoff-shaped packet.
`
	case "agent":
		return `Usage:
  abra agent bootstrap [path] [--agent <agent>] [--scope repo:project] [--force] [--no-mcp]
  abra agent init [path] [--agent <agent>] [--scope repo:project] [--force] [--dry-run] [--json]
  abra agent verify [path] --scope repo:project [--agent <agent>] [--files-only] [--strict] [--json]
  abra agent install codex
  abra agent status

Agent commands wire AI clients to the same source-backed Abra scope. Some
clients have automated install helpers; other clients use abra mcp JSON or the
HTTP MCP URL. ` + "`abra agent ready`" + ` remains a non-mutating compatibility alias
for ` + "`abra agent verify`" + `.
`
	case "model":
		return `Usage:
  abra model
  abra model local
  abra model openai --api-key-stdin
  abra model compatible --base-url <url> --model <model> --dimensions <size> [--api-key-stdin]
  abra model up
  abra model status [--json] [--force]
  abra model logs [--force]
  abra model down [--force]

Model commands configure or operate Abra's embedding and reranker providers.
Abra defaults to a self-hosted local embedding runner, and any compatible
embedding provider can replace it when configured. The OSS local default is
Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0 with optional Qwen/Qwen3-Reranker-0.6B-GGUF:Q8_0.
`
	case "brain":
		return `Usage:
  abra brain [--scope repo:demo]
  abra brain status --scope repo:demo
  abra brain doctor --scope repo:demo
  abra brain review --scope repo:demo [--limit 50] [--json]
  abra brain scorecard --scope repo:demo [--limit 50] [--json]
  abra brain anchor-backfill --scope repo:demo [--dry-run|--propose] [--json]
  abra brain maintain --scope repo:demo [--dry-run|--propose] [--json]
  abra brain dossier <entity> --scope repo:demo [--mode fast|balanced|deep] [--json]
  abra brain explain <trace-id>

Brain CLI is a small operator surface over the same governed MCP tools used by
agents. Review and scorecard are read-only. Anchor backfill and maintain default
to dry-run; --propose creates reviewable learning proposals and never promotes
trusted memory directly. Dossier inspects one entity with claims, relations,
anchors, trust, and next action.
Compatibility top-level commands such as ask, context, recall, and compose remain
available for automation, but they call MCP tools instead of private REST brain paths.
`
	case "govern":
		return `Usage:
  abra govern status --scope repo:demo
  abra govern doctor --scope repo:demo
  abra govern approvals --scope repo:demo
  abra govern approvals request --scope repo:demo --action agent_write --reason "..."
  abra govern approvals approve <approval-id>
  abra govern observe "operator note" --scope repo:demo --propose
  abra govern learning list --scope repo:demo [--status pending]
  abra govern learning accept <proposal-id> [--reason "..."]
  abra govern learning reject <proposal-id> [--reason "..."]
  abra govern learning apply <proposal-id> [--approval-id <approval-id>]

Governance commands handle health, approvals, observations, and learning review.
They do not bypass source authority, approval gates, or evidence requirements.
`
	case "plugin", "plugins":
		return `Usage:
  abra plugin list [--json]
  abra plugin contract [--json]
  abra plugin mcp template --scope team:docs --output connector.json
  abra plugin mcp validate --scope team:docs --mcp-url https://mcp.example.com/mcp --tool export_documents
  abra plugin mcp register --scope team:docs --mcp-url https://mcp.example.com/mcp --tool export_documents
  abra plugin webhook sample --scope team:docs --connector knowledge-base

Plugins are adapter contracts, not core bypasses. External systems normalize
data into Abra documents through MCP, HTTP ingest, or signed webhooks. Abra core
keeps transform, chunking, embedding, graph extraction, citations, approvals,
memory health, and decision gates.
`
	case "ingest":
		return `Usage:
  abra ingest . [--code] [--continue-on-error] [--quiet]
  abra ingest ./notes.md
  abra ingest --text "source-backed content" [--title Intro] [--approval-id approval...]
  abra ingest --git https://github.com/owner/repo.git [--ref main] [--wait]

Manual document flags:
  --scope          memory scope, default repo:<current-git-root-or-folder>
  --text           document text
  --file           read document text from file
  --title          document title
  --source-url     stable source URL
  --source-type    default markdown
  --approval-id    approved agent_write request for enforced production writes

Source ingestion flags:
  --path           local repository or directory to ingest from the CLI
  --git, --repo    remote Git repository URL to clone through the worker
  --ref, --branch  Git ref for --git
  --include        comma-separated document globs, default **/*.md
  --exclude        comma-separated exclude globs
  --code           also ingest code intelligence from supported code files
  --code-include   comma-separated code globs for --code
  --code-exclude   comma-separated code exclude globs for --code
  --max-file-bytes skip matched files larger than this before reading, default 1048576
  --include-generated
                  include generated/minified/lock files that are skipped by default
  --wait           wait for the queued worker job when using --git or watch
  --tracked        register a local path source and queue a worker job; path must be worker-visible
  --no-wait        return immediately after queueing a tracked local path ingestion job
  --wait-timeout   max wait for queued worker jobs, default 1m
  --freshness-seconds
                  mark the source due when the last successful refresh is older than this many seconds
  --schedule       worker refresh cadence: @hourly, @daily, or @every <N><s|m|h|d>
  --direct         force direct local ingestion through /ingest/documents/batch
  --continue-on-error
                  keep direct local ingestion running after per-file failures; exits nonzero if any fail
  --quiet         suppress direct local batch progress in human output
  --timeout        HTTP timeout for direct local/file/text ingest, default 10m
`
	case "config":
		return `Usage:
  abra config show [--json]
  abra config path
  abra config model local [--base-url http://host.docker.internal:8080/v1] [--runner-image image@sha256:...] [--pull-policy missing] [--readiness-timeout 10s] [--reranker-base-url <url> --reranker-model <model>]
  abra config model qwen3
  abra config model openai --api-key-stdin
  abra config model compatible --base-url <url> --model <model> --dimensions <size> [--api-key-stdin] [--reranker-base-url <url> --reranker-model <model>]

Config edits the Abra runtime env file used by abra up. It intentionally only
exposes core runtime settings needed for local operation and embedding/reranker connection.
Use these commands, or abra setup, to connect Abra to the embedding model used
for retrieval and working memory; common local/compatible paths do not
require manual env file editing.
Use --embedding-batch-max-items and --embedding-batch-max-tokens to tune provider
request size when a local model times out or a scaled compatible provider can
handle larger batches.
Use --api-key or --api-key-stdin when your embedding endpoint requires auth,
--embedding-timeout to tune provider calls, and --provider-concurrency to limit
parallel provider requests.
For custom reranking, add --reranker-base-url and --reranker-model; the provider
is inferred as compatible. Use --reranker-api-key when the reranker has a
separate key, or --no-reranker to leave reranking disabled.
After changing model config, restart with: abra down && abra up
Check readiness with: abra doctor
After changing embedding providers, sync important sources again for reliable vector recall.
`
	case "models":
		return `Usage:
  abra model up [--recreate] [--port 8080] [--pull-policy missing] [--startup-timeout 10m] [--allow-production-local-embeddings] [--model-id <repo>] [--model <served-name>]
  abra model status [--json] [--force]
  abra model logs [--force]
  abra model down [--force]

` + "`abra models`" + ` is a compatibility alias for ` + "`abra model`" + `.
Starts and manages the built-in local Qwen3 embedding runner for the default
local setup. Optional rerankers are configured separately through compatible
reranker provider settings. Abra keeps the binary lightweight: model weights stay in Docker's
model cache, while the CLI owns startup, health checks, and lifecycle.

Operational flags:
  --model-id       model repository used by the local runner
  --model          served model name exposed by the local runner
  --dimensions     embedding dimensions, default 1024
  --image          llama.cpp server image; use a digest-pinned image in production
  --pull-policy    Docker image pull policy: missing, always, or never
  --readiness-timeout timeout for one readiness request, default 10s
  --startup-timeout total wait per model runner, default 10m
  --allow-production-local-embeddings
                  explicitly allow the local runner in production; production also requires digest-pinned images
  --cache-dir      host model cache directory
  --container      Docker container name
  --base-url       local OpenAI-compatible base URL
  --port           host port for the embedding server, default 8080
  --publish-addr   host address to publish on, default 127.0.0.1
  --reranker-port  host port for the reranker server, default 8081
  --force          inspect or manage the local runner even when current config uses a non-local provider
`
	case "ui", "dashboard":
		return `Usage:
  abra setup

The previous interactive UI command was removed. Use abra setup for guided
onboarding, or abra up for non-interactive stack startup.
`
	case "watch", "source":
		return `Usage:
  abra watch local --scope repo:demo --path . [--include "**/*.md"] [--code] [--freshness-seconds 3600] [--schedule "@every 1h"] [--wait]
  abra watch git --scope repo:demo --git https://github.com/owner/repo.git [--ref main] [--freshness-seconds 3600] [--wait]
  abra source mcp --scope team:docs --mcp-url https://mcp.example.com/mcp --tool export_documents --dry-run
  abra source mcp --scope team:docs --mcp-url https://mcp.example.com/mcp --tool export_documents [--arguments-json '{"collection":"docs"}'] [--document-source-type markdown] [--bearer-token-env TOKEN_ENV] [--header-env Header=ENV] [--allow-private-network] [--schedule "@every 10m"] [--wait]

This creates or updates a source config, then enqueues an ingestion job.
The OSS worker supports markdown, local_repo, git_repo, and MCP HTTP sources
whose configured tool returns normalized Abra documents. External systems such
as Jira, Confluence, Slack, and Drive can either expose an MCP document-export
tool or push normalized documents through the HTTP/MCP ingestion API.
Use --dry-run or --validate with MCP sources to call the upstream MCP tool,
validate its normalized documents, and exit without registering or queueing.
Use --bearer-token-env and --header-env Header=ENV for MCP credentials.
Use --allow-private-network only for trusted local/dev MCP connectors.
Use --freshness-seconds for max source age and --schedule for @hourly, @daily,
or @every <N><s|m|h|d> worker refresh cadence. Manual sync still bypasses the
due check.
Use --wait-timeout or ABRA_CLI_WAIT_TIMEOUT for slow local model or large repo runs.
`
	case "connectors", "connector":
		return `Usage:
  abra connectors [--scope repo:demo] [--limit 50] [--json]
  abra connectors list [--scope repo:demo] [--limit 50] [--json]
  abra connectors mcp inspect --scope team:docs --mcp-url https://mcp.example.com/mcp [--bearer-token-env TOKEN_ENV] [--header-env Header=ENV] [--allow-private-network]
  abra connectors mcp template --scope team:docs [--output knowledge-base.connector.json]
  abra connectors mcp add --scope team:docs --mcp-url https://mcp.example.com/mcp [--tool export_documents] [--manifest connector.json] [--wait] [--verify]
  abra connectors mcp validate --scope team:docs --mcp-url https://mcp.example.com/mcp --tool export_documents [--manifest connector.json] [--arguments-json '{"collection":"docs"}'] [--document-source-type markdown] [--bearer-token-env TOKEN_ENV] [--header-env Header=ENV] [--allow-private-network]
  abra connectors mcp register --scope team:docs --mcp-url https://mcp.example.com/mcp --tool export_documents [--manifest connector.json] [--arguments-json '{"collection":"docs"}'] [--document-source-type markdown] [--bearer-token-env TOKEN_ENV] [--header-env Header=ENV] [--allow-private-network] [--schedule "@every 10m"] [--wait] [--verify] [--verify-query "runbook"]
  abra connectors status <source-config-id> [--json]
  abra connectors logs <source-config-id> [--limit 20] [--json]
  abra connectors sync <source-config-id> [--wait] [--json]
  abra connectors webhook sample --scope team:docs --connector knowledge-base [--secret-env ABRA_WEBHOOK_SECRET]
  abra connectors webhook sign --payload-json '{"scope":"team:docs",...}' --secret-env ABRA_WEBHOOK_SECRET
  abra connectors webhook test --scope team:docs --connector knowledge-base [--secret-env ABRA_WEBHOOK_SECRET] [--json]

Lightweight connector onboarding commands backed by existing source configs.
MCP inspect calls upstream tools/list so operators can find export tool names.
MCP template prints or writes a repeatable manifest with ACL metadata passthrough hints.
MCP add is the operator-guided flow: inspect when --tool is omitted, validate,
then register and optionally --wait or --verify.
MCP validate calls the upstream MCP tool and exits without registering.
MCP register creates the MCP source config and queues the initial ingestion job.
Use --manifest connector.json to keep URL, tool, env refs, schedule, authority,
and verification query in one declarative file that maps to source_configs.
Connector status/logs/sync are aliases for source status/logs/sync, kept here so
connector operators do not need a separate UI.
Webhook sample/sign/test help overlay connectors verify signed push ingestion.
Existing abra source mcp commands continue to work unchanged.
`
	case "sources":
		return `Usage:
  abra sources [--scope repo:demo] [--limit 50] [--json]
  abra sources sync <source-config-id> [--scope repo:demo] [--wait] [--wait-timeout 10m] [--json]
  abra sources backfill <source-config-id> [--scope repo:demo] [--approval-id approval...] [--wait] [--wait-timeout 10m] [--json]
  abra sources status <source-config-id> [--json]
  abra sources logs <source-config-id> [--limit 20] [--json]
  abra sources pause <source-config-id> [--json]
  abra sources resume <source-config-id> [--approval-id approval...] [--json]

Lists configured ingestion sources, queues sync/backfill jobs, inspects source status/logs, or pauses/resumes a source.
Resume and backfill may require approval when enforcement is active.
`
	case "jobs":
		return `Usage:
  abra jobs --scope repo:demo [--source-config-id source...] [--limit 20] [--json]

Lists worker ingestion jobs for a scope.
`
	case "approvals", "approval":
		return `Usage:
  abra approvals [--scope repo:demo] [--status pending] [--limit 50] [--json]
  abra approvals request --scope repo:demo --action agent_write [--target-type document] [--target-id doc...] [--reason "..."] [--payload-json '{}'] [--metadata-json '{}'] [--json]
  abra approvals approve <approval-id> [--reason "..."] [--decided-by operator] [--metadata-json '{}'] [--json]
  abra approvals reject <approval-id> [--reason "..."] [--decided-by operator] [--metadata-json '{}'] [--json]

Creates, lists, approves, or rejects bounded operator approval requests for
production approval enforcement. Use the returned approval id with commands
that accept --approval-id.
`
	case "observe":
		return `Usage:
  abra observe "Agents should rerun release checks before tagging" [--scope repo:demo]
  abra observe --text "..." --type episode --source-url file://notes.md --confidence 0.7 [--json]
  abra observe "..." --propose --scope repo:demo --source-url file://runbook.md
  abra observe conversation --file transcript.md --scope repo:demo [--propose] [--all-turns]

Captures a raw observation. Observations are scoped, searchable, audited, and
not trusted claims until a review/promote flow explicitly turns them into one.
Use --propose to immediately create a pending learning proposal from the
captured observation without writing trusted memory. Conversation capture
defaults to preference-like user turns; use --all-turns for full episodic capture.
`
	case "observations", "episodes":
		return `Usage:
  abra observations --scope repo:demo [--query release] [--type episode] [--status raw] [--limit 20] [--json]
  abra observations propose <observation-id> --scope repo:demo [--claim "..."] [--source-url file://runbook.md] [--json]
  abra episodes --scope repo:demo

Lists raw episodic observations for a scope.
The propose subcommand creates a pending learning proposal targeting the
observation. Accepted proposals do not auto-write claims; apply them explicitly
through POST /learning/proposals/:proposalId/apply or MCP apply_learning_proposal.
`
	case "think":
		return `Usage:
  abra think "question" --scope repo:demo [--agent codex] [--entity Name] [--as-of YYYY-MM-DD] [--include-historical] [--mode fast|balanced|deep] [--limit n] [--max-queries n] [--token-budget n] [--include-unverified] [--brief] [--agent-output] [--synthesize] [--json]

Asks the governed brain layer. Returns a cited answer, verification, gaps,
memory health, and an agent decision gate. Use --brief for a compact answer
with top evidence and trust status, or --agent-output for a handoff-shaped
answer. Use --synthesize only when the server has optional synthesis enabled;
deterministic source-backed output remains the default.
Use --mode fast to cap fanout, balanced for default behavior, or deep for wider
evidence without enabling synthesis.

If the answer has no context, run:
  abra scope
  abra sync . --code --scope <scope-from-abra-scope>
  abra ask "question" --scope <scope-from-abra-scope>

For AI-client readiness, run:
  abra agent verify . --scope <scope-from-abra-scope> --agent codex
  abra doctor
`
	case "eval":
		return `Usage:
  abra eval brain --file brain-eval.json [--agent codex] [--synthesize] [--json]

Runs a deterministic brain-answer quality suite through MCP brain_think. Cases can
require verdicts, agent decisions, citation refs, answer text, forbidden text,
and claim evidence anchors. The command exits non-zero when any case fails.
`
	case "recall":
		return `Usage:
  abra recall "query" --scope repo:demo [--include-unverified] [--json]

Runs hybrid lexical/vector retrieval over source-backed memory.
`
	case "compose":
		return `Usage:
  abra compose "task" --scope repo:demo [--agent codex] [--entity Name] [--file path] [--changed-file path] [--language go] [--as-of YYYY-MM-DD] [--include-historical] [--mode fast|balanced|deep] [--limit n] [--max-queries n] [--token-budget n] [--include-unverified] [--hook before_task] [--brief] [--agent-output] [--prompt] [--persist-learning] [--json]

Builds a task-specific working-memory packet for AI coding agents. Human output
includes the decision gate, retrieval quality, health signals, validation plan,
allowed next actions, and suggested steps. Use --mode fast to cap fanout,
balanced for default behavior, or deep for wider evidence. Use --brief for a
compact operator view, --agent-output for a handoff-shaped packet, or --prompt
to also print the prompt-ready context window for another AI client. By default
compose is read-only; --persist-learning writes actionable learning suggestions
as pending review proposals and requires write access.
`
	case "memory":
		return `Usage:
  abra memory status --scope repo:demo [--json]
  abra memory health --scope repo:demo [--json]
  abra memory doctor --scope repo:demo [--json]

Shows memory health for a scope directly from the CLI: document/claim/source
coverage, ingestion backlog, conflicts, learning proposals, and health signals.
Use status for a compact operator view and doctor when you need every signal.
`
	case "scope":
		return `Usage:
  abra scope [path] [--json]

Prints the stable memory scope for a project path and shows the exact commands
and agent prompt to use. Use this when an AI client says Abra has no context:
first run agent verify with the printed scope. If server_ready is true but
agent_ready is false, repair MCP/token/client restart before syncing. Only
run sync when verify proves the exact scope or source-backed memory is missing.
`
	case "agents":
		return `Usage:
  abra agent bootstrap [path] [--agent codex] [--scope repo:project] [--force] [--no-mcp]
  abra agent init [path] [--agent codex] [--scope repo:project] [--force] [--dry-run] [--json]
  abra agent verify [path] --scope repo:project [--agent codex] [--files-only] [--strict] [--json]

` + "`abra agents`" + ` is a compatibility alias for ` + "`abra agent`" + `.
Writes repo-local AI agent instruction files that point every client at the
same Abra scope. It creates AGENTS.md for agent-neutral instructions and
CLAUDE.md importing AGENTS.md so Claude Code reads the same guidance without
duplicating content. Existing files are skipped unless --force is set.

` + "`abra agent bootstrap`" + ` is the one-command Codex-ready path: it writes
agent instructions, ingests the repo with the exact scope and --code, installs
the Abra MCP endpoint into Codex, and verifies source-backed working memory
unless --no-mcp is set.

` + "`abra agent verify`" + ` checks AGENTS.md, CLAUDE.md, the MCP endpoint, required
agent tools, discover_scopes for the exact project scope, and a lightweight
working_memory_compose packet with source-backed context. Use it when an AI
client says Abra has no context. Use --files-only for CI checks that should not
contact a live Abra MCP server. Use --strict when warning-level compatibility
checks should fail the command. ` + "`abra agent ready`" + ` and ` + "`abra agents ready`" + `
remain non-mutating compatibility aliases for verify.
`
	case "mcp", "mcp-config":
		return `Usage:
  abra mcp [--token-env ABRA_API_TOKEN] [--literal-token]
  abra mcp status [--json] [--strict]
  abra mcp install-codex [--token-env ABRA_API_TOKEN]

` + "`abra mcp`" + ` prints generic remote HTTP MCP client JSON. By default it
uses bearer_token_env_var instead of writing a literal token; use --literal-token
only for legacy clients that cannot read bearer-token env vars.
` + "`abra mcp status`" + ` checks API readiness, required MCP tools, Codex MCP registration,
shell token env, and Codex Desktop launch env without printing token values.
` + "`abra mcp install-codex`" + ` installs Abra into Codex as a streamable HTTP MCP
server using the Codex CLI, stores the bearer-token env var name, validates the
Abra MCP endpoint, and sets the token for the current macOS launch environment
when available. Fully quit and reopen Codex Desktop after installing or changing
the token env. No manual Codex config editing is required for this common path.

Common Codex path:
  abra setup
  abra doctor
  cd /path/to/project
  abra agent bootstrap --agent codex
  fully quit and reopen Codex Desktop
  abra agent verify . --scope <scope-from-abra-scope> --json

Run abra mcp status when Codex cannot see Abra; it checks API/MCP readiness,
Codex registration, token env, and launch environment. Run abra doctor for the
full model/API/MCP preflight.
`
	case "doctor":
		return `Usage:
  abra doctor [--json] [--strict] [--token-env ABRA_API_TOKEN]

Checks local commands, runtime env permissions, embedding model config, local
embedding readiness, API readiness, MCP tools, and Codex token-env hints. Use it
after abra setup, after changing model config, and before rerunning
abra mcp install-codex when Codex cannot connect. Use --strict for CI or release
preflight checks that should exit non-zero when any check is not ok.
`
	case "setup":
		return `Usage:
  abra setup
  abra setup --yes
  abra setup --yes --no-models
  abra setup --local
  abra setup --openai --api-key-stdin
  abra setup --compatible --embedding-base-url <url> --embedding-model <model> --dimensions <size> [--api-key-stdin] [--reranker-base-url <url> --reranker-model <model>]
  abra setup --provider compatible --embedding-base-url <url> --embedding-model <model> --dimensions <size>
  abra setup --yes --no-start

Guided first-run onboarding. It checks prerequisites, creates the runtime env,
chooses the embedding provider used for retrieval/vector search, and can start
the local stack. This does not configure a chat model or LLM answer model. The
default local provider uses the built-in Qwen3 embedding runner, which abra up
starts automatically and abra model up/status manages directly.
Common local and compatible embedding paths are configured with
CLI commands only; no manual env file editing is required.

If setup writes config but later commands cannot embed, run abra doctor first.
For the default local provider, abra up starts the model runner automatically;
use abra model status and abra model up when you want to inspect or repair it
directly.

Runtime source:
  - release-installed CLIs use the published runtime bundle for that version
  - source checkouts use local Compose files when commands run from the checkout
  - custom runtime archives require ABRA_SOURCE_URL and ABRA_SOURCE_SHA256
  - ABRA_ALLOW_MUTABLE_RUNTIME_SOURCE=1 is only for local development tests of
    mutable main-branch source downloads

Common setup flags:
  --embedding-base-url  embedding provider base URL
  --base-url            legacy alias for --embedding-base-url during setup
  --embedding-model     embedding request model name; this is not a chat model
  --model               provider selector or legacy embedding model alias, not a chat model
  --dimensions          embedding dimensions; required for unknown compatible models, inferred for known provider presets
  --embedding-timeout   provider timeout, default 10m for local and 30s for compatible
  --embedding-batch-max-items max embedding inputs per provider request, default 6 local and 16 compatible
  --embedding-batch-max-tokens estimated embedding tokens per provider request, default 3000 local and 6000 compatible
  --provider-concurrency provider call concurrency, default 1 for local and 4 for compatible
  --api-key             embedding provider API key
  --api-key-stdin       read embedding provider API key from stdin
  --reranker-base-url   compatible reranker provider base URL
  --reranker-model      compatible reranker request model name
  --reranker-api-key    reranker provider API key; defaults to embedding key for compatible providers
  --reranker-api-key-stdin read reranker provider API key from stdin
  --reranker-timeout    reranker timeout, default 10m for local and 30s for compatible
  --no-reranker         leave reranking disabled for a custom provider
  --no-models           do not start the local model runners
  --skip-models         alias for --no-models
  --no-start            write config but do not start the Abra stack
  --skip-up             alias for --no-start
`
	case "install", "up", "quickstart", "demo":
		return `Usage:
  abra setup
  abra up [--no-models]
  abra demo
  abra install

abra setup is the guided first-run path. abra up starts the default local
embedding runner when the env uses EMBEDDING_PROVIDER=local, then starts the
local Docker Compose stack non-interactively: Postgres, migrations, API, and
worker. Use --no-models when you intentionally manage the model endpoints
yourself. abra install is kept as a compatibility alias for abra setup; the curl
installer is what installs the CLI binary.
`
	case "upgrade", "update":
		return `Usage:
  abra upgrade
  abra upgrade --version vX.Y.Z

Re-runs the public install script into the current binary directory. Set
ABRA_INSTALL_SCRIPT to override the installer URL or ABRA_INSTALL_DIR when
running the install script directly.
`
	case "uninstall":
		return `Usage:
  abra uninstall --yes

Removes the Abra CLI binary only. It does not remove Docker containers,
volumes, env files, runtime bundles, or memory data.
`
	default:
		return usage()
	}
}
