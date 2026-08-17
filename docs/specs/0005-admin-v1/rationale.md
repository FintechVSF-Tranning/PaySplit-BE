# 0005. Admin v1: Rationale

## Context

The PaySplit group expense splitting platform requires internal administrative oversight to support operations, investigate user disputes, moderate compromised or malicious accounts, and track infrastructure health. Without administrative capabilities, operators cannot intervene when accounts violate policies or suffer security breaches, and platform health cannot be reliably evaluated.

The administrative surface must balance operational visibility with strict data privacy. Operators need to view user identities, active sessions, and group debt obligations to resolve support tickets, but exposing raw authentication hashes or unmasked bank account numbers would create severe security liabilities. Additionally, administrative status changes (such as suspending or locking an account) must immediately disconnect compromised users by revoking active sessions and rotating refresh tokens in a single atomic database operation.

System monitoring requires both human readable business summaries (for the web admin dashboard) and standardized metrics pipelines (for Prometheus and Grafana alerting), alongside explicit liveness and readiness health probes for container orchestration.

## Options considered

### Option 1: Dedicated Clean Architecture Admin Module with Dual Metrics (Recommended)

Implement an `internal/modules/admin/` module with domain, usecase, repository, and HTTP delivery layers. Protect routes via existing JWT verification and `RequireRole("admin")` middleware. Execute status transitions with atomic session revocations and audit log records in PostgreSQL. Expose `GET /api/v1/admin/system/overview` for the administrative UI and `/metrics` for Prometheus scraping.

**Pros**:
- Consistent with the modular clean architecture of PaySplit.
- Enforces strict role based access control and immediate session revocation.
- Guarantees complete audit traceability and data masking at the repository layer.
- Serves both web dashboards and infrastructure monitoring tools natively.

**Cons**:
- Requires implementing dedicated repository queries and usecase orchestration for admin operations.

### Option 2: Generic CRUD Direct Database Administration Tool

Use an off the shelf database GUI or generic administrative CRUD interface (such as pgAdmin or Forest Admin) connected directly to PostgreSQL without application level API endpoints.

**Pros**:
- Fast initial setup without writing backend Go code.

**Cons**:
- Bypasses application level domain invariants (session revocation, audit logging, and role guards).
- Exposes raw sensitive database fields (including password hashes and unmasked bank details) to operators.
- Lacks integration with application level Prometheus metrics and customized business dashboard queries.

## Rationale

Option 1 is selected because administrative actions on a financial coordination platform carry high security and compliance obligations. Modifying an account status to suspended or locked must immediately invalidate active sessions and refresh tokens across all devices to prevent unauthorized actions. Directly updating database rows via external tools risks omitting session revocation and audit logging.

Building dedicated application endpoints under `/api/v1/admin/*` protected by `RequireRole("admin")` ensures that every moderation action is validated, logged in `admin_audit_logs`, and sanitized to mask sensitive bank details. Providing both structured JSON overview endpoints and Prometheus format metrics satisfies both dashboard UI requirements and automated cluster monitoring.

## References

**Project sources**:
- `CLAUDE.md`: clean architecture conventions and middleware wiring in `internal/transport/http/middleware/auth.go`.
- `docs/Product_Requirement_Document.md`: functional requirements 4.1.20 (View List Account), 4.1.21 (View Account Details), 4.1.22 (Update Account Status), and 4.1.23 (System Monitoring).
- `docs/screen_flow.md`: Module 5 Admin screen definitions and API specifications.
- `docs/specs/0001-auth-account-v1/index.md`: user role, account status enum, and database session revocation model.
- `db/migrations/000001_init_schema.up.sql`: `users`, `sessions`, `session_refresh_tokens`, `groups`, `debts`, and `admin_audit_logs` schema definitions.

**Practices & standards**:
- Principle of least privilege and role based access control (RBAC).
- Immediate session revocation on security status change.
- Sensitive data masking for payment credentials (OWASP guidelines).
- Kubernetes / container health check pattern (separation of liveness and readiness).
