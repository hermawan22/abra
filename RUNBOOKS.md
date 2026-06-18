# Abra Operational Runbooks

These runbooks define the v1 operator path for self-hosted Abra. They describe what can be done with the current OSS runtime and where an enterprise overlay should add automation.

## Operating Principles

- Treat Postgres as the system of record. It contains source text snippets, claims, graph records, embeddings, audit events, source configs, and ingestion job history.
- Prefer managed Postgres snapshots for production. Use logical dumps for portable backups, pre-upgrade validation, and restore drills.
- Keep `EMBEDDING_DIMENSIONS` stable for a database. The v1 schema uses `vector(1536)` unless a migration changes it.
- Stop or pause workers before destructive maintenance. API reads can stay online during logical backup, but strict restore drills and embedding migrations should run against an isolated database first.
- Record the `x-request-id`, deployment version, migration run, and source config IDs in incident notes.
- Treat approval requests as review records. Set `ABRA_APPROVAL_MODE=enforce` when agent-facing credentials should be blocked from supported risky memory operations unless they provide a matching approved `approval_id`.

## Backup

### Managed Postgres

Use the cloud or platform backup policy as the primary backup. Minimum requirements:

- Automated daily snapshots.
- Point-in-time recovery if the provider supports it.
- Retention long enough for internal audit and incident response.
- Regular restore drill into a non-production database.
- Backup encryption and access control matching the sensitivity of source-derived snippets and embeddings.

Before an upgrade or schema migration, also take an explicit snapshot and export a logical backup:

```sh
DATABASE_URL=postgres://... npm run ops:backup
```

The helper writes a custom-format dump under `ABRA_BACKUP_DIR` or `backups` by default, then validates the dump with `pg_restore --list`. Set `ABRA_DRY_RUN=1` to print the backup plan without writing a dump.

### Docker Compose Postgres

For the bundled Compose database, prefer the same helper against the mapped Postgres port when possible:

```sh
DATABASE_URL=postgres://abra:abra@localhost:5433/abra npm run ops:backup
```

If the database port is not exposed, run `pg_dump` inside the Compose network or use the managed Postgres snapshot mechanism. Validate any dump is readable with:

```sh
pg_restore --list backups/abra_YYYYMMDD_HHMMSS.dump > /tmp/abra_restore_manifest.txt
```

## Restore

Always test restore into an isolated database before replacing production.

1. Provision a fresh Postgres instance with `pgvector` available.
2. Stop API and worker processes that point to the target database.
3. Restore the dump.
4. Run migrations. They must be idempotent against the restored schema.
5. Start the API.
6. Run smoke checks.
7. Start the worker only after recall, source configs, and ingestion history look correct.

Example:

```sh
createdb abra_restore
ABRA_RESTORE_DUMP=backups/abra_YYYYMMDD_HHMMSS.dump \
ABRA_RESTORE_DATABASE_URL=postgres://user:password@localhost:5432/abra_restore \
ABRA_MIGRATE_CMD=/app/abra-migrate \
ABRA_DRY_RUN=0 \
npm run ops:restore-drill
```

`ops:restore-drill` is dry-run by default. It validates the dump manifest, refuses to restore into the same value as `DATABASE_URL` unless `ABRA_ALLOW_RESTORE_TO_DATABASE_URL=1`, and runs `ABRA_MIGRATE_CMD` only when explicitly provided.

Smoke checks:

```sh
curl -fsS "$ABRA_BASE_URL/healthz"
curl -fsS -H "Authorization: Bearer $ABRA_API_TOKEN" "$ABRA_BASE_URL/readyz"
curl -fsS -H "Authorization: Bearer $ABRA_API_TOKEN" "$ABRA_BASE_URL/sources/configs"
curl -fsS -H "Authorization: Bearer $ABRA_API_TOKEN" "$ABRA_BASE_URL/ingestion/jobs"
```

Then ingest a small known document, recall it, and confirm citations and scopes are correct.

## Reindex

Use reindexing when query plans degrade, vector search slows after heavy churn, or an index is suspected to be corrupted. Reindexing does not change embeddings or claim content.
Because `memory/compose` evaluates stored agent-action policies on the request path, include policy indexes in the same maintenance window when policy churn is high.

Run during a maintenance window:

```sh
DATABASE_URL=postgres://... npm run ops:reindex
DATABASE_URL=postgres://... ABRA_DRY_RUN=0 npm run ops:reindex
```

The helper is dry-run by default, covers the indexes created by the bundled migrations, skips missing indexes unless `ABRA_REINDEX_STRICT=1`, and uses `REINDEX INDEX CONCURRENTLY` unless `ABRA_REINDEX_CONCURRENTLY=0`.

If the database does not support concurrent reindex for the index type or the command fails, stop the worker, run `REINDEX INDEX`, then restart the worker. For broad corruption recovery, prefer restoring from backup and replaying trusted source ingestion.

After reindex:

- Check `GET /readyz`.
- Run `ABRA_BASE_URL=... ABRA_API_TOKEN=... npm run smoke:selfhost`.
- Compare recall latency and result counts against the pre-maintenance baseline.

## Embedding Model Migration

The current v1 schema stores 1536-dimensional vectors. Changing to another dimension is a schema migration, not a configuration-only change.

Safe path for same-dimension model changes:

1. Create a database backup.
2. Deploy the new embedding provider configuration in a staging environment.
3. Re-ingest approved sources through the existing ingestion path so chunks, claims, entities, and relations keep source lineage.
4. Run the eval suite against baseline queries.
5. Promote only if recall quality and latency are acceptable.

Safe path for dimension changes:

1. Add a migration that changes every vector column and vector index consistently.
2. Deploy to staging with an empty or restored copy of production.
3. Re-ingest all approved sources.
4. Rebuild vector indexes.
5. Run the eval suite and smoke suite.
6. Cut over only after restore rollback is tested.

Do not mix embeddings with different dimensions in the same vector columns. Do not update `EMBEDDING_DIMENSIONS` alone and expect existing rows to remain valid.

## Incident Checks

Use this sequence before making changes during an incident.

### Approval-Required Operation

Use this before any agent-initiated forget, broad-scope write, source authority change, ACL change, connector enablement, or backfill.

1. Confirm the agent created an approval request with `action`, `scope`, `reason`, target fields, and proposed payload.
2. List pending requests:

   ```sh
   curl -fsS -H "Authorization: Bearer $ABRA_OPS_TOKEN" \
     "$ABRA_BASE_URL/approvals?scope=$SCOPE&status=pending"
   ```

3. Verify requester identity, source evidence, affected scope, target record, rollback path, and downstream systems affected by the change.
4. Approve or reject the request:

   ```sh
   curl -fsS -X POST -H "Authorization: Bearer $ABRA_OPS_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"decided_by":"oncall","decision_reason":"reviewed evidence and rollback"}' \
     "$ABRA_BASE_URL/approvals/$APPROVAL_ID/approve"
   ```

5. If approved, retry supported OSS risky memory operations with `approval_id`. For connector, ACL, and backfill actions outside OSS Abra, perform the change with controlled operator automation.
6. Record the approval ID, operation `x-request-id`, result, and rollback notes in the incident or change record.
7. If rejected or expired, leave the risky operation unapplied and notify the requester through the surrounding workflow.

### Signed Webhook Ingestion

Use this when a Jira, Confluence, Slack, or custom connector overlay pushes normalized documents into Abra.

1. Configure `ABRA_WEBHOOK_SECRETS` on the API deployment and keep the secret in the connector runtime.
2. Build the exact JSON body the connector will send to `POST /ingest/webhooks`.
3. Compute HMAC SHA-256 over the raw body and send it as `x-abra-signature: sha256=<hex>`.
4. Include stable `connector_kind`, `event_type`, `delivery_id`, `scope`, `source_type`, `source_url`, `title`, `content`, and authority metadata.
5. Confirm the response returns `accepted` and document IDs, then recall one expected claim in the same scope.
6. If the endpoint returns `invalid_webhook_signature`, compare the raw bytes used for signing with the raw HTTP body and rotate the webhook secret if needed.

### ACL Policy Change

Use this when a gateway, connector, team, or agent needs a new Abra scope/resource decision.

1. Create and approve an `acl_change` approval request with target type `acl_policy`.
2. Upsert the policy with `POST /acl/policies` and include the approved `approval_id` when `ABRA_APPROVAL_MODE=enforce`.
3. Verify the intended principal with `POST /acl/decision`.
4. Verify at least one unrelated principal returns `deny`.
5. Record the policy name, approval ID, and gateway rollout reference in the change record.

### API Unavailable

1. Check `GET /healthz` and `GET /readyz`.
2. Check gateway or ingress logs for authentication failures and rate limits.
3. Check API logs by `x-request-id`.
4. Check Postgres connectivity and connection saturation.
5. Confirm the latest migration job completed.
6. If only `/mcp` fails, verify the client sends `Authorization: Bearer <key>` or `x-api-key`.

### Recall Quality Degraded

1. Check whether `EMBEDDING_PROVIDER`, `EMBEDDING_MODEL`, and `EMBEDDING_DIMENSIONS` changed.
2. Inspect recent `ingestion_jobs` for failed or partial source refreshes.
3. Confirm source configs are still `active` and scoped correctly.
4. Compare recall against known eval queries.
5. Check whether relevant claims became `deprecated`, `expired`, or `challenged`.
6. Re-ingest one affected source in staging before bulk re-ingestion.

### Ingestion Stuck

1. Check `GET /ingestion/jobs` for `failed`, long-running, or repeated error rows.
2. Check worker logs for connector, filesystem, or database errors.
3. Confirm source configs have valid `config`, `base_url`, and `status`.
4. Confirm worker deployment has the same embedding credentials as the API.
5. Stop worker before manual database repair.
6. Resume with one known source before enabling broad ingestion.

### Data Exposure or Wrong Scope

1. Stop worker ingestion.
2. Disable affected source configs or remove them from the private connector overlay.
3. Identify affected `scope`, `source_config_id`, and `source_url` values.
4. Preserve audit events and gateway logs.
5. Create or locate an approval request for any forget, broad deprecation, source authority change, ACL update, or connector disablement that will mutate production state.
6. Deprecate or delete records only after incident owner approval.
7. Rotate API keys if a client or connector leaked access.

### Embedding Provider Outage

1. Confirm API readiness and Postgres health separately.
2. Check provider error rates and credentials.
3. Pause non-critical ingestion to avoid repeated failed jobs.
4. Keep recall available from existing vectors if the API is healthy.
5. Resume ingestion after the provider is stable and run targeted eval checks.

## Release Runbook

Before deploy:

```sh
npm test
go test ./...
docker build -t abra:local .
helm lint deploy/helm
helm template abra ./deploy/helm >/tmp/abra-rendered.yaml
```

Deploy:

1. Take a backup or managed snapshot.
2. Run migration once.
3. Roll API.
4. Roll worker.
5. Run the self-host smoke suite.
6. Run the dogfood gate so `repo:abra` self-ingestion and working-memory composition are proven against the candidate build.
7. Run a small eval subset.
8. Watch metrics and ingestion jobs for one worker cycle.

Rollback:

1. Stop worker.
2. Roll API back to the prior image.
3. If the migration is incompatible with the prior image, restore the pre-deploy backup into a fresh database and point services at it.
4. Run smoke checks before restarting the worker.

## Enterprise Automation Boundary

The OSS runbook is intentionally manual and transparent. Commercial editions or deployment overlays should add:

- Scheduled backups and restore drills.
- Admin UI actions for source pause, retry, backfill, and export.
- Enforced approval workflow for destructive repairs and risky agent operations.
- SIEM-specific transforms, routing, retention policy, and delivery monitoring beyond the generic audit sink.
- SSO/RBAC around operational endpoints.
- Automated embedding migration jobs with progress and rollback status.
