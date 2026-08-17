# 0004. Notification and background queue v1

**Date**: 2026-08-17
**Status**: Proposed

## Summary

Notification and background queue v1 adds a job engine and notification system to PaySplit. It uses River Queue on PostgreSQL to run background tasks safely with automated retries. It uses Firebase Cloud Messaging to send push notifications to mobile devices, and stores in app notifications for users to read later. Active devices register their token on sign in or token refresh, and uninstalled devices are cleaned up automatically.

## Rationale

Reasoning and options considered: see [rationale.md](rationale.md).

## Requirements

**User stories**:

1. As an active group member, I want to receive push notifications when bills are added or payments are confirmed so that I stay informed even when the app is closed.
2. As a payer, I want to receive friendly debt reminders so that I can settle my balance on time.
3. As an authenticated user, I want an in app notification inbox where I can view my history, see unread badges, and mark items as read.
4. As the system, I want background tasks to process reliably through a durable queue so that network timeouts or server restarts never drop jobs.

**Acceptance criteria**:

1. **AC-1**: Active sessions can store an optional FCM registration token during sign in or update it through `PUT /api/v1/users/me/fcm-token`. The token is bound to the current session and cleared when the session is revoked or expires. Because spec 0001 enforces at most one nonrevoked session per user, each user has at most one active FCM token at any time.
2. **AC-2**: Background job execution is powered by River Queue running on the PostgreSQL 18 connection pool (`pgx/v5`). River database migrations run safely during system bootstrap with `rivermigrate.New`.
3. **AC-3**: Dispatching a notification creates a row in the `notifications` table (with user ID, type, title, body, JSONB payload, and creation timestamp) and enqueues a `send_notification` River job in the same database transaction. The job payload carries `NotificationID uuid` only; the worker reads title, body, and payload from the stored notification record.
4. **AC-4**: When processing a `send_notification` job, the worker looks up the single active FCM token for the target user (the one nonrevoked session). If no active session or token exists, the worker completes without error. If the token is invalid or unregistered (`messaging.IsRegistrationTokenNotRegistered`), the worker clears the dead token from the session and completes. Transient database or network errors return an error to trigger River exponential backoff retry.
5. **AC-5**: Push message builders produce localized Vietnamese titles, bodies with thousand separated VND money format (for example `1.500.000`), structured metadata (`group_id`, `bill_id`, `payment_id`), and Flutter navigation click action identifiers.
6. **AC-6**: Authenticated users can list their notifications with offset pagination (`GET /api/v1/notifications`, default `page=1`, default `page_size=20`, max `page_size=50`), query their unread count (`GET /api/v1/notifications/unread-count`), mark a single item as read (`PATCH /api/v1/notifications/{id}/read`), and mark all items as read (`PATCH /api/v1/notifications/read-all`). Cross user notification access returns `404` to avoid leaking notification existence.
7. **AC-7**: Graceful application shutdown stops accepting new HTTP requests first, drains running River workers within the shutdown deadline, and closes the database connection pool cleanly.

## Decision

**Chosen option**: Use PostgreSQL backed River Queue (`github.com/riverqueue/river`) for background job processing and Firebase Cloud Messaging (`firebase.google.com/go/v4`) for push notifications. Implement Clean Architecture with `internal/modules/notification/` for in app delivery and usecases, `internal/platform/queue/river` for queue infrastructure, and `internal/platform/notification/fcm` for external FCM integration.

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Data model sketch

| Entity | Key fields | Constraints and relationships |
|---|---|---|
| `sessions` | `fcm_token` (TEXT) | Optional token bound to the active session. At most one nonrevoked session per user (spec 0001 partial unique index), so at most one active FCM token per user. |
| `notifications` | UUID v7 `id`, `user_id`, `type`, `title`, `body`, `payload` (JSONB), `read_at`, `created_at` | References `users(id)` ON DELETE CASCADE. Check constraints on `type` (1 to 60 chars), `title` (1 to 255 chars), `body` (1 to 1000 chars). Index on `(user_id) WHERE read_at IS NULL` and `(user_id, created_at DESC)`. |
| `river_*` | Internal River tables | Managed automatically via River migrator on PostgreSQL. |

**Notification types** (initial set, other modules may add values):

| Type value | Trigger | Payload keys |
|---|---|---|
| `bill_finalized` | Captain finalizes a bill | `group_id`, `bill_id`, `amount` |
| `debt_reminder` | Reminder scheduler scans stalled debts | `group_id`, `bill_id`, `debt_id`, `amount` |
| `payment_proof_submitted` | Payer submits payment proof | `group_id`, `payment_id`, `amount` |
| `payment_confirmed` | Creditor confirms a payment | `group_id`, `payment_id`, `amount` |
| `payment_rejected` | Creditor rejects a payment | `group_id`, `payment_id` |

**JSONB payload contract** (minimal schema for Flutter deep linking):

```json
{
  "group_id": "uuid | null",
  "bill_id": "uuid | null",
  "debt_id": "uuid | null",
  "payment_id": "uuid | null",
  "amount": "int64 | null",
  "click_action": "string"
}
```

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/api/v1/users/me/fcm-token` | PUT | `fcm_token: string` | message | Live Session | 400 VALIDATION_FAILED, 401 UNAUTHORIZED |
| `/api/v1/notifications` | GET | `page` (default 1), `page_size` (default 20, max 50) | paginated items, total, pager | Live Session | 401 UNAUTHORIZED |
| `/api/v1/notifications/unread-count` | GET | none | `unread_count: int64` | Live Session | 401 UNAUTHORIZED |
| `/api/v1/notifications/{id}/read` | PATCH | `id` path param | message | Live Session | 401 UNAUTHORIZED, 404 NOT_FOUND |
| `/api/v1/notifications/read-all` | PATCH | none | message | Live Session | 401 UNAUTHORIZED |

### Value sourcing

| Action | Value produced / displayed | Source |
|---|---|---|
| Send notification | In app notification record | Created from `PushMessage` title, body, and payload |
| Send notification | River queue job | Enqueued with `NotificationJobArgs { NotificationID uuid }` referencing the stored notification record |
| Process notification job | Device FCM token | Fetched from active `sessions` table where `user_id = $1` and `revoked_at IS NULL` |
| Process notification job | Dead token cleanup | Clears `fcm_token` in `sessions` when FCM returns unregistered error |
| List notifications | Paginated notification list | Queried from `notifications` table ordered by `created_at DESC` |
| Get unread count | Unread number | Count query on `notifications` where `user_id = $1` and `read_at IS NULL` |
| Mark as read | Updated `read_at` timestamp | Set to `now()` where `id = $1` and `user_id = $2` and `read_at IS NULL` |

### Key invariants

- An in app notification record is always created when a notification is dispatched, ensuring users can see historical activity even if push delivery fails.
- River jobs are inserted in the same database transaction as the business event whenever possible, preventing lost or orphan notifications.
- A user can only view, count, and mark as read their own notifications. Cross user access returns `404` to avoid leaking existence.
- Push notification payload amounts are formatted using integer arithmetic with thousand dot separators in Vietnamese Dong.
- Because spec 0001 enforces one active session per user, the notification worker always queries at most one FCM token. Multi device push is a future follow up.
- When FCM is disabled (no credentials configured), the notification usecase creates the in app record but does not enqueue a push delivery job. The notification worker is not registered with River.

### Security model

- Endpoints require valid access token and active session middleware (`liveAuth`).
- Notifications are scoped to the authenticated `user_id` from the verified token context.
- Firebase credentials are read from environment variables or secure credential files and never logged or exposed via APIs.

### Configuration required

- `FIREBASE_CREDENTIALS_FILE`: Path to Google Firebase Service Account JSON file (optional in development; when both `FIREBASE_CREDENTIALS_FILE` and `FIREBASE_CREDENTIALS_JSON` are unset, FCM is disabled and push jobs are not enqueued).
- `FIREBASE_CREDENTIALS_JSON`: Raw JSON string of Service Account credentials (alternative for cloud environments).
- `FCM_TIMEOUT_SECONDS`: Timeout duration for FCM API requests, defaults to 5 seconds.
- `RIVER_WORKER_COUNT`: Number of concurrent River worker goroutines, defaults to 5.
- `RIVER_FETCH_COOLDOWN_MS`: Cooldown between River fetch polls in milliseconds, defaults to 100.

### Critical test scenarios

- Happy path: Dispatch notification, verify in app record exists, verify River job enqueued and push sent, verifies **AC-3**, **AC-5**.
- Dead token cleanup: Worker receives unregistered token error from FCM, verifies token cleared from database without failing the job, verifies **AC-4**.
- Transient failure retry: Worker encounters temporary network timeout, verifies error returned for River retry, verifies **AC-4**.
- Mark as read flow: Query unread count, mark specific item as read, mark all as read, verify counts update, verifies **AC-6**.
- Auth security: User attempts to read or modify another user notification, receives error, verifies **AC-6**.

## Build plan

1. Create the next sequential migration in `db/migrations/` (never edit existing migrations) for `sessions.fcm_token` column and `notifications` table with check constraints, indexes, and the initial notification type values, satisfies **AC-1**, **AC-3**.
2. River Queue platform adapter, migrator, worker registry with configurable concurrency, and graceful lifecycle wiring in bootstrap, satisfies **AC-2**, **AC-7**.
3. FCM push notification platform client with disabled mode detection, payload builders, and dead token cleanup, satisfies **AC-4**, **AC-5**.
4. In app notification repository, service usecase with `NotificationJobArgs { NotificationID }`, and River enqueuer, satisfies **AC-3**, **AC-4**, **AC-6**.
5. HTTP delivery handlers with pagination defaults and limits, routes registration under `/api/v1`, and unit and integration tests, satisfies **AC-1**, **AC-6**.

## Consequences

**Positive**:
- Background tasks run reliably without blocking client requests.
- Transactional coupling with PostgreSQL avoids lost jobs and phantom state.
- Users receive real time push alerts and keep an in app history.

**Negative / tradeoffs**:
- Slightly higher database connection pool usage for River workers.
- Dependency on external Google Firebase infrastructure for push delivery.

**Neutral**:
- Requires Firebase Service Account configuration for push notifications to function.

## Follow-up

- [ ] Connect Bill and OCR finalization events to automated member push notifications.
- [ ] Implement Reminder Scheduler cron worker to scan stalled debts and dispatch periodic reminders.
- [ ] Support multi device push notifications when the session model allows multiple active sessions per user.
