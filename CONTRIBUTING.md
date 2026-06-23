# Contributing

Thanks for considering a contribution to Abra.

## Development

Prerequisites:

- Go 1.25.11 or newer.
- Node.js 24 or newer.
- Docker or a compatible container runtime for full release-gate checks.
- Postgres with `pgvector` for local integration testing.

Run the fast checks:

```sh
go test ./...
npm test
```

Read the Architecture map before moving boundaries or adding user-facing
surface:

- `docs/ARCHITECTURE.md` explains package ownership, request paths, and where
  core changes belong.
- `docs/EXTENSIONS.md` explains the extension boundary.
- `docs/PLUGIN_AUTHORING.md` explains how community plugins should emit
  normalized documents without bypassing governance.

Run the full local release gate when Docker is available:

```sh
ABRA_RELEASE_PROFILE=full ABRA_RELEASE_MANAGE_STACK=1 npm run release:gate
```

## Agent-Assisted Contributions

Contributors may use any AI coding agent that supports repository instructions
or MCP. Agent-assisted work must follow the same review bar as human-written
changes:

- read `AGENTS.md` before changing code;
- use the exact project scope printed by `abra scope`;
- call Abra MCP working-memory tools before architecture changes or broad code
  edits when an Abra server is available;
- keep generated changes reviewable and scoped;
- cite source-backed project context in pull request notes when it materially
  influenced the implementation;
- never commit prompts, transcripts, tokens, API keys, private context, or
  generated artifacts that are not part of the reviewed source tree.

Agent output is not a substitute for tests, code review, or release gates. The
person opening the pull request remains responsible for correctness, security,
licensing, and maintainability.

## Pull Request Guidelines

- Keep changes scoped to one behavior or documentation topic.
- Respect the feature freeze in `docs/FEATURE_FREEZE.md`; new user-facing
  features should fit the frozen CLI surface or live behind a plugin/overlay
  contract.
- Keep package ownership intact. If a hotspot budget fails, split by command
  family, store aggregate, MCP tool family, or brain capability instead of
  increasing the budget.
- Add or update tests when changing runtime behavior.
- Keep public APIs, MCP tools, migrations, and deployment manifests backward-compatible unless the change is explicitly documented.
- Do not commit secrets, database dumps, embeddings, source-system exports, audit logs, or organization-specific policies.
- Use generic examples in docs. Avoid company names, real repository URLs, tokens, private domains, incident details, or customer data.
- Keep docs short and canonical. Prefer `connect`, `sync`, `ask`, `context`,
  `agent`, and `model` in new docs; mention compatibility aliases only when
  maintaining older automation.
- For local/private denylist terms, run `npm run check:oss` with `ABRA_OSS_PRIVATE_CONTEXT_PATTERNS` instead of committing private words to the scanner.

## Design Principles

- Claims should remain source-cited, scoped, auditable, and status-aware.
- Model-provider logic belongs behind generic interfaces.
- Deployment-specific connector, identity, and compliance behavior belongs in
  extensions or overlays.
- Production behavior should fail closed for auth, approvals, rate limits, and unsafe configuration.

## Plugin contributions

Community plugins are welcome when they stay provider-neutral at the Abra core
boundary. Add source-specific behavior as an adapter, MCP exporter, signed
webhook producer, or private overlay. Plugins should output normalized
documents, include dry-run validation steps, and avoid secrets or raw private
exports in committed examples.
