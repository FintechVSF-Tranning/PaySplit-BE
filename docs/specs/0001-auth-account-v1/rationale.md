# Rationale for auth and account v1

## Context

> ⚠️ Premise note: Custom authentication is a high risk area because token rotation, replay handling, account recovery, and session revocation each create security failure modes. PaySplit already has an internal auth schema and adapters, so this decision keeps the scope to email and password, uses proven libraries for primitives, and makes PostgreSQL authoritative rather than expanding into OAuth, phone verification, or MFA.

The existing auth module has a modular domain, repository, usecase, HTTP delivery, bcrypt adapter, and JWT adapter. Login issues only an access token. Registration is incomplete, refresh sessions are not wired, account status is not enforced through a live session, and the published OpenAPI surface covers only two placeholder routes.

The mobile screen flow and PRD require registration, verification, sign in, recovery, sign out, change password, and profile management. The prototype also needs one active device, immediate revocation, a personal Gmail sender, Cloudinary avatars, and a local bank list. Existing PostgreSQL 17 cannot generate UUID v7 natively, while the engineer requires database generated identifiers.

This feature handles PII but remains a nonproduction prototype. It does not hold funds, verify phone ownership, or verify a real bank account.

## Options considered

### Fix only registration and login

Complete the two existing handlers and keep access token only authentication. (basis: the current OpenAPI surface and existing auth code)

**Pros**:

1. Smallest implementation and no schema expansion.
2. Preserves the existing route names.

**Cons**:

1. Does not satisfy the PRD session, recovery, verification, sign out, or profile flows.
2. Cannot immediately revoke access or enforce one active device.

### Hosted identity provider

Move identity, verification, recovery, and sessions to a hosted auth provider. (basis: hosted identity operations practice)

**Pros**:

1. Reduces the amount of security sensitive code operated by the team.
2. Mature providers offer email workflows, OAuth, MFA, and abuse controls.

**Cons**:

1. Replaces the existing internal schema and adapters rather than extending them.
2. Adds provider migration, mobile SDK, callback, and pricing concerns beyond the prototype requirement.
3. Personal Gmail SMTP and the confirmed custom profile workflow would still need application adapters.

### Extend the modular auth system

Keep domain ownership in PaySplit, add database sessions and token history, and isolate Gmail and Cloudinary behind interfaces. (basis: existing module boundaries, the PRD, and PostgreSQL transaction guidance)

**Pros**:

1. Matches the existing Go modular architecture and data model.
2. Gives immediate revocation, strict refresh reuse detection, and one device enforcement.
3. Keeps provider replacement local to adapters.

**Cons**:

1. The team owns security testing and ongoing auth maintenance.
2. Every protected request reads PostgreSQL.
3. The feature is larger than completing the existing two handlers.

## Rationale

Extending the existing module is the most direct path that satisfies the confirmed product behavior without adding a hosted identity migration. PostgreSQL constraints and short transactions protect the concurrency rules, while bcrypt, the JWT library, cryptographic random tokens, Gmail SMTP, and Cloudinary provide bounded primitives rather than hand built cryptography or media storage.

Direct route replacement is acceptable because the API is an unpublished prototype. The implementation still lands in end to end slices so each user journey can be tested before the next one expands the module. PostgreSQL 18 is chosen because it supplies native `uuidv7()` and avoids a custom database extension.

Gmail SMTP is intentionally a prototype choice. Resend was evaluated and has a simpler transactional API, but it cannot use a personal `@gmail.com` address as a verified sender. The `Mailer` interface is required so production can move away from personal Gmail without changing auth usecases.

## References

### Project sources

1. `docs/Product_Requirement_Document.md`, auth and profile requirements.
2. `docs/screen_flow.md`, mobile screens and proposed API grouping.
3. `docs/openapi.yaml`, current placeholder route surface.
4. `db/migrations/000001_init_schema.up.sql`, existing identity and session schema.
5. `.agents/skills/supabase-postgres-best-practices/`, schema, index, FK, transaction, and advisory lock guidance.

### Practices and standards

1. Store password and bearer token verifiers as one way hashes.
2. Rotate refresh tokens atomically and revoke the token family on replay.
3. Keep external network calls outside database transactions.
4. Use partial unique indexes for filtered uniqueness and index every foreign key used for joins or cascade.
5. Use generic recovery responses to prevent account enumeration.

### Links

1. PostgreSQL 17 UUID functions: https://www.postgresql.org/docs/17/functions-uuid.html
2. PostgreSQL 18 UUID functions: https://www.postgresql.org/docs/18/functions-uuid.html
3. Google App Passwords: https://support.google.com/accounts/answer/185833
4. Gmail SMTP configuration: https://support.google.com/a/answer/176600
5. go-mail: https://github.com/wneessen/go-mail
6. Cloudinary image format support: https://cloudinary.com/documentation/image_format_support
7. Cloudinary upload API and destroy method: https://cloudinary.com/documentation/image_upload_api_reference
8. Cloudinary incoming transformations: https://cloudinary.com/documentation/eager_and_incoming_transformations
9. Cloudinary Go integration: https://cloudinary.com/documentation/go_integration
10. Pure Go WebP codec: https://github.com/deepteams/webp
11. VietQR bank snapshot: https://vietqr.app/banks.json
12. Supabase PostgreSQL best practices skill: https://www.skills.sh/supabase/agent-skills/supabase-postgres-best-practices
13. Google MCP Toolbox for databases: https://github.com/googleapis/mcp-toolbox
14. Cloudinary MCP servers: https://cloudinary.com/documentation/cloudinary_llm_mcp
