# 0004. Notification and background queue v1: Rationale

## Context

PaySplit requires reliable, asynchronous communication to coordinate group expenses across mobile devices:
- Alerting group members when a bill is created or updated.
- Notifying payers when expenses are allocated and when payment reminders trigger.
- Alerting creditors when payment proofs are submitted, and notifying payers upon confirmation or rejection.
- Providing an in-app notification center where users can review historical activity and unread alerts.
- Processing long-running background tasks (push notification delivery, OCR document parsing, stale debt scanning) without blocking synchronous HTTP request threads or dropping jobs on process restarts.

## Options considered

### Option 1: External Broker (Redis / RabbitMQ with Celery or Asynq)

Introduce a dedicated external message broker (such as Redis or RabbitMQ) and worker pool to process background jobs.

**Pros**:
- High memory-based throughput and low queue latency.
- Dedicated queue tooling and monitoring ecosystem.

**Cons**:
- Adds a new stateful infrastructure component to provision, secure, monitor, and back up.
- Lacks transactional coupling with PostgreSQL. Creating a business record (e.g. finalizing a bill) and enqueuing its notification cannot be atomic without complex two-phase commit or outbox patterns.

### Option 2: PostgreSQL-backed River Queue (Chosen)

Use River Queue (`github.com/riverqueue/river`), a fast background job engine for Go backed by PostgreSQL and the `pgx/v5` connection pool.

**Pros**:
- Uses the existing PostgreSQL 18 database and connection pool with zero additional infrastructure.
- Supports transactional enqueuing (`InsertTx`), guaranteeing that jobs are queued if and only if the business transaction commits.
- Built-in worker concurrency controls, exponential backoff, dead job inspection, and periodic cron scheduling.

**Cons**:
- Slightly higher disk I/O on PostgreSQL compared to pure in-memory queues (well within PaySplit scale requirements).
- Requires schema tables managed by River migrator.

### Option 3: Firebase Cloud Messaging (FCM) for Push Delivery (Chosen)

Integrate Google Firebase Cloud Messaging (`firebase.google.com/go/v4`) for cross-platform push delivery to Flutter clients on Android and iOS.

**Pros**:
- Industry standard for mobile operating systems, capable of waking devices in background or terminated states.
- Direct registration token targeting per session and broadcast topic delivery (`all_users`).
- Native error classifications (`IsRegistrationTokenNotRegistered`) allow instant cleanup of dead tokens.

**Cons**:
- Relies on external Google Cloud infrastructure and service account credentials.

## Rationale

River Queue is selected because transactional enqueuing eliminates dual-write race conditions between PostgreSQL records and queue jobs. Reusing the PostgreSQL pool avoids operational complexity and costs for an external broker during the MVP and growth phases. Firebase Cloud Messaging provides direct, reliable mobile notifications on Android and iOS with native Flutter integration.

## References

**Project sources**:
- `Product_Requirement_Document.md`, section 4.2.1 (Automated Debt Reminders) and section 6.1 (Architecture Components)
- `docs/specs/0001-auth-account-v1/index.md` (Session and device lifecycle)
- `docs/specs/0003-bill-ocr-v1/index.md` (Durable River jobs for OCR and bill finalization notifications)

**Practices and standards**:
- Transactional Outbox and Queuing pattern with PostgreSQL
- Graceful shutdown lifecycle for HTTP servers and queue workers
- Dead token pruning pattern for push notification providers
