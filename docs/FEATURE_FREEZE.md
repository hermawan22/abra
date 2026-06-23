# Feature Freeze

Abra's pre-OSS public surface is frozen around a small agent-first core.

## Frozen Product Shape

Abra is:

- an agent-first governed external brain;
- MCP-first for agent cognition;
- CLI-backed for install, setup, operator inspection, maintenance, and eval;
- source, model, and agent agnostic;
- backed by Postgres and pgvector;
- local-first by default through a compatible embedding runner;
- extensible through normalized plugin contracts.

Abra is not:

- a web app;
- a source-specific connector bundle;
- a chat UI;
- a generic automation platform;
- a place to encode private business logic.

## Canonical Operator CLI Surface

New operator-facing work must fit one of these commands:

- `setup`
- `up`
- `down`
- `doctor`
- `scope`
- `connect`
- `sync`
- `agent`
- `model`
- `brain`
- `govern`
- `plugin`

`ask` and `context` remain supported operator/script fallbacks for inspecting
brain output, but the canonical agent path is MCP: `discover_scopes`,
`working_memory_compose`, `brain_think`, and the brain quality tools.

Compatibility commands can remain, but they should not be promoted as the main
path in new documentation.

## Allowed Core Work

- Reliability, security, and production hardening.
- Better source normalization, transform, extraction, recall, and governance.
- Provider-neutral MCP brain, operator CLI, and plugin contract improvements.
- HTTP transport hardening when required by MCP, CLI, gateways, or automation.
- Tests, diagnostics, and install quality.
- Documentation that makes the frozen surface clearer.

## Plugin Or Overlay Work

Use an extension boundary for:

- source-specific connectors;
- OAuth, ACL, workspace, or customer mapping;
- private approval policy;
- business-model-specific behavior;
- deployment-specific routing, gateways, and compliance hooks.

Plugins and overlays may be powerful, but they must feed Abra's normalized
document contract instead of bypassing core governance.

## Review Checklist

Before adding a feature, answer:

- Does this help every agent, source, or model?
- Can this be expressed as normalized documents or a generic policy?
- Would this expose private business context in OSS?
- Does it add a new command when an existing command group can own it?
- Does it keep install and first use simple?

If the answer is unclear, keep it out of core.
