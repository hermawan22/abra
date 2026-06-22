# Feature Freeze

Abra's pre-OSS public surface is frozen around a small agent-first core.

## Frozen Product Shape

Abra is:

- a CLI-first governed brain for AI agents;
- source, model, and agent agnostic;
- backed by Postgres and pgvector;
- local-first by default through Qwen embeddings;
- extensible through normalized plugin contracts.

Abra is not:

- a web app;
- a vendor-specific connector bundle;
- a chat UI;
- a generic automation platform;
- a place to encode private business logic.

## Canonical CLI Surface

New user-facing work must fit one of these commands:

```text
setup
up
down
doctor
scope
connect
sync
ask
context
agent
model
brain
govern
plugin
```

Compatibility commands can remain, but they should not be promoted as the main
path in new documentation.

## Allowed Core Work

- Reliability, security, and production hardening.
- Better source normalization, transform, extraction, recall, and governance.
- Provider-neutral CLI, MCP, HTTP, and plugin contract improvements.
- Tests, diagnostics, and install quality.
- Documentation that makes the frozen surface clearer.

## Plugin Or Overlay Work

Use an extension boundary for:

- vendor-specific connectors;
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
