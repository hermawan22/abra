## Summary

<!-- What changed and why? -->

## Verification

- [ ] `go test ./...`
- [ ] `npm test`
- [ ] `helm lint ./deploy/helm`
- [ ] `helm template abra ./deploy/helm`
- [ ] `go run ./cmd/abra doctor`
- [ ] Relevant smoke/eval/release gate documented below

## Security And OSS Hygiene

- [ ] No secrets, `.env` contents, database dumps, embeddings, audit records, or source-system exports
- [ ] Public docs use generic examples only
- [ ] New endpoints, MCP tools, migrations, or deployment settings are documented
- [ ] Risky memory, approval, ACL, or source-authority changes fail closed

## Notes

<!-- Risks, follow-ups, or skipped checks. -->
