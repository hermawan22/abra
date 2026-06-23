# Database Migrations

Abra ships with a squashed OSS baseline migration for new installations:

```text
001_init.sql
```

The baseline creates the complete schema used by the current release, including
documents, chunks, claims, evidence, conflicts, audit events, source configs,
ingestion jobs, graph tables, policies, approvals, summaries, learning
proposals, rate limits, traces, eval history, temporal claim metadata, and
evidence anchor spans.

## Baseline Policy

`001_init.sql` is the public baseline for fresh databases. Keep it stable after
release so existing deployments can rely on the filename recorded in
`schema_migrations`.

During pre-release development, the baseline may be regenerated to keep the OSS
repository easy to install and review. After a release is cut, schema changes
are append-only and must be added as new migration files.

## New Migration Naming

```text
NNN_short_descriptive_slug.sql
```

Rules:

- use a zero-padded three-digit sequence;
- do not skip or reuse numbers;
- use lowercase snake_case after the sequence;
- add a short `-- Migration NNN: ...` header at the top;
- make changes idempotent where practical with `IF NOT EXISTS`, guarded
  constraints, or data-safe checks;
- add a new migration for fixes instead of editing a released migration.

The next migration after the current baseline is:

```text
002_<description>.sql
```

## Validation

`npm test` runs `scripts/abra-migration-check.mjs`, which validates migration
sequence, filename shape, unique numbers, and standard headers.
