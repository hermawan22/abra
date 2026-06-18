# Service Engineering Conventions

## Testing

- Services should run contract tests before release when API behavior changes.
- API changes should include success, validation, auth, rate-limit, and failure cases when applicable.
- Shared client or schema changes should include compatibility coverage.

## Shared Contracts

- Service code should reuse shared contracts before introducing local schemas.
- Shared contract changes require approval because they affect multiple integrations.
- Breaking API changes should include migration notes.

## Operational Quality

- Observable behavior changes should be verified with smoke checks and logs.
- Request handlers should preserve useful error messages without leaking secrets.
- Operational commands should be scriptable and return stable exit codes.
