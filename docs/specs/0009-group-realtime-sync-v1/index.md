# 0009. Group realtime sync v1

**Date**: 2026-08-26
**Updated**: 2026-09-03
**Status**: In Progress
**Target release**: V1

## Summary

PaySplit keeps one authenticated SSE connection for the signed in Flutter session. Home, group detail, bill detail, OCR, lock, and settlement updates all reach that connection. Roster keeps its durable delta and `/sync` recovery, while other surfaces receive a small invalidation and refetch their REST source.

The shared PostgreSQL listener from spec 0010 remains the only `LISTEN` connection owned by each API process. This feature adds `user_events` to its fixed channel registry, so `group_events`, `bill_events`, and user invalidations still share one database session.

Reasoning and options: see [rationale.md](rationale.md).

## Requirements

### User stories

1. As a group member, I want the member list on my phone to update when someone joins or leaves so I do not pull to refresh.
2. As a signed in user, I want Home, the open group, and the open bill to update from one live connection so I do not open a stream per screen.
3. As an operator, I want the realtime feature to keep one PostgreSQL `LISTEN` connection per API process.

### Acceptance criteria

Roster protocol, shipped and unchanged:

1. **AC-1**: Mỗi nhóm có `roster_version` đơn điệu tăng. Dãy version liền mạch và đúng thứ tự commit, kể cả khi nhiều mutation chạy song song.
2. **AC-2**: Mọi mutation roster ghi đúng một dòng `group_events` trong cùng transaction với thay đổi dữ liệu. Transaction bị rollback không để lại sự kiện và không tiêu tốn version.
3. **AC-3**: `pg_notify` được gọi bên trong transaction, nên client không bao giờ nhận sự kiện của một transaction bị rollback.
4. **AC-4**: `GET /groups/{id}` trả `version` và `caller_membership_id`, đọc trong một transaction `REPEATABLE READ` để version khớp đúng danh sách thành viên đi kèm.
5. **AC-5**: `GET /groups/{id}/sync?since=N` trả `mode=delta` khi client còn đủ gần, `mode=snapshot` khi `since<=0`, khi `since` lớn hơn version hiện tại, khi nhật ký đã bị dọn qua mốc `since`, hoặc khi delta bị cắt vì chạm giới hạn.
6. **AC-6**: `GET /groups/{id}/events` xác thực caller là thành viên active, đăng ký nhận sự kiện trước khi đọc trạng thái ban đầu, và không phát trùng sự kiện đã nằm trong phần khởi tạo.
7. **AC-7**: Legacy group stream phát `close` kèm lý do khi chạm tuổi thọ tối đa, khi nhóm bị giải tán, hoặc khi chính caller không còn là thành viên.
8. **AC-8**: Thân frame SSE của sự kiện nhật ký mang đúng shape với phần tử `events` của `/sync`, để client dùng chung một hàm giải mã.
9. **AC-9**: Payload rời server mang `avatar_url`, không bao giờ mang `avatar_object_key` hoặc danh sách audience nội bộ.
10. **AC-10**: Client giữ roster `version` cục bộ. Version nhảy cóc thì gọi `/sync`; version nhỏ hơn hoặc bằng thì bỏ qua.
11. **AC-11**: Client kết nối lại với exponential backoff có jitter và đồng bộ lại khi app resume.
12. **AC-12**: Nhật ký cũ hơn `GROUP_EVENT_RETENTION_DAYS` được dọn định kỳ. Client tụt xa hơn ngưỡng đó nhận snapshot.

User stream amendment:

13. **AC-13**: Mỗi API process dùng shared listener của spec 0010 để `LISTEN group_events`, `bill_events`, và `user_events` trên đúng một connection khi listener healthy. Mở hoặc đóng SSE client không acquire một listener connection khác. Listener disconnect đóng mọi local SSE subscription trước khi reconnect. `GET /api/v1/users/me/events` chỉ trả `200` và `ready` khi local listener healthy; nếu chưa ghi header mà listener unhealthy thì trả `503 REALTIME_UNAVAILABLE`.
14. **AC-14**: `GET /api/v1/users/me/events` yêu cầu JWT và session còn sống. Mỗi subscribe có server generated `stream_id`. Server đăng ký stream mới ở trạng thái paused rồi phát replace control qua `user_events`. Publish lỗi gỡ stream mới, giữ stream cũ, và trả `503` trước SSE headers. Publish thành công làm mọi API process, gồm process hiện tại, đóng stream cũ có cùng `sid`, trừ stream mang `replacement_stream_id`. Control commit sau cùng thắng khi hai subscribe chạy đồng thời. Sign out, sign in thay thế thiết bị, password reset, password change, refresh reuse, và admin suspend hoặc lock lấy mọi revoked `sid` bằng `UPDATE ... RETURNING id`, rồi phát `session_ended` trong cùng transaction thu hồi session.
15. **AC-15**: User stream chỉ phát các frame công khai đã đóng băng trong spec này: `ready`, `roster`, `invalidate`, `ocr.updated`, `heartbeat`, và `close`. Stream không phát full bill, receipt bytes, item text, bank data, `avatar_object_key`, `audience_user_ids`, hoặc raw internal error.
16. **AC-16**: Server subscriber buffer có capacity `64`. `ready` luôn là public frame đầu tiên; event đến sau subscribe nhưng trước `ready` được xếp hàng. Buffer đầy làm stream đóng với `backpressure`. Sau mỗi `ready`, gồm initial connect và reconnect, Flutter refetch mọi registered realtime interest trước khi coi data state là live. Event đã xếp hàng hoặc đến trong lúc refetch được buffer theo AC-18.
17. **AC-17**: Flutter có đúng một session scoped user stream owner nằm trên authenticated navigation shell và không `autoDispose` theo page. Owner bắt đầu sau khi token của session sẵn sàng, đóng khi logout, session expiry, `paused`, `hidden`, hoặc `detached`, giữ nguyên ở trạng thái `inactive`, và được tạo lại khi `sessionRevisionProvider` đổi hoặc app `resumed`. Page scoped providers chỉ đăng ký local realtime interest trong memory bằng provider key và gỡ nó qua `ref.onDispose`; chúng không mở SSE và không gửi network `sub/unsub`.
18. **AC-18**: Khi stream lỗi, nhận `close`, listener reset, app resume, hoặc `503`, client dùng một reconnect single flight. Backoff là `1, 2, 4, 8, 15, 30` giây với jitter từ `70%` đến `130%`, reset chỉ sau `ready`. `max_connection_age` reconnect ngay nhưng vẫn chạy ready refetch. Invalidation trùng trong cửa sổ `250 ms` được gộp theo refetch target và hợp nhất tập consumer, không theo event type. Client giữ tối đa `256` pending refetch target, latest OCR state theo bill, và `64` roster frame theo group; tràn giới hạn đánh dấu full mounted refetch, còn tràn roster đánh dấu group phải `/sync`. Refetch lỗi giữ interest ở trạng thái dirty và retry cùng backoff cho tới khi thành công; last good state vẫn hiển thị.
19. **AC-19**: Audience được xác định trong cùng transaction với mutation và chỉ nằm trong PostgreSQL notify envelope. Join hoặc reactivate gửi cho toàn bộ active members sau mutation, gồm member mới. Leave hoặc remove gửi final roster event cho active members sau mutation cộng target user. Archive chụp active user IDs trước khi deactivate. Mọi event sau đó chỉ gửi cho audience active được mutation tương ứng xác định.
20. **AC-20**: Bill create, edit, draft delete, OCR candidate apply, review, finalize, bulk finalize success, và void gọi `pg_notify('user_events', ...)` bên trong cùng transaction đã ghi committed bill state. Payload là một `invalidate` có `group_id`, `resource_id=bill_id`, committed `resource_version`, và type cụ thể. Không có đường publish sau commit cho các mutation này.
21. **AC-21**: Mọi OCR transition `queued`, `processing`, `succeeded`, hoặc `failed` ghi `ocr_jobs` và gọi `pg_notify('bill_events', ...)` trong cùng transaction. Internal bill event mang `group_id` và audience active. Public `ocr.updated` mang `group_id`, `bill_id`, `job_id`, status, attempts, nullable cleaned error, warnings, `created_at`, `updated_at`, và nullable `completed_at`. OCR waiter GET current bill and OCR summary trước khi chờ event, sau mỗi ready, và khi timeout 60 giây.
22. **AC-22**: Bill submission lock gọi `pg_notify('user_events', ...)` trong cùng transaction với `groups.bill_submission_locked_at`, không tăng `roster_version`. Settlement create payment, submit proof, confirm, reject, và debt reminder phát invalidation theo matrix trong spec. Mọi transition đổi debt status lấy distinct affected `bill_id` từ các debt row đã khóa. Settlement invalidation không có `resource_version` vì settlement không đổi `bills.version`. Worker chỉ phát realtime invalidation khi nó thật sự đổi persistent payment hoặc debt state; notification only worker không đánh thức các provider ngoài notification feature.
23. **AC-23**: Legacy group and bill SSE routes ở lại trong giai đoạn cutover. Flutter không bao giờ mở hai mode cùng lúc. `401` refresh token đúng một lần rồi sign out nếu thất bại. `404` hoặc `501` trước user stream `ready` fallback sang legacy. `429` dùng `Retry-After`. `503`, timeout, hoặc network close giữ user mode và reconnect theo AC-18; sau khi user stream từng nhận `ready` trong session hiện tại, nó không fallback legacy. Route chỉ chuyển sang `410 STREAM_REPLACED` sau khi minimum supported app version đã dùng user stream và production telemetry không còn legacy request trong 30 ngày. Flutter gửi `X-App-Version` từ package metadata trên user và legacy SSE request. Response `410` trả `replacement=/api/v1/users/me/events`; OpenAPI giữ route là deprecated cho tới khi support window kết thúc.

## Decision

**Chosen option**: One session scoped user SSE, explicit transaction sourced audiences, and small invalidations over the shared PostgreSQL listener.

Keep the shipped roster log and `/sync`. Add `user_events` as a third channel on the existing shared listener, not as another connection and not as a database table. The user hub indexes streams by `user_id`, `sid`, and `stream_id`; it does not cache a mutable group set. Mutation transactions decide their audience, while public frames omit that internal routing data.

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Data model sketch

No new table or migration is required.

| Store | Role | Change |
|---|---|---|
| `groups.roster_version` | Cursor for durable roster events | Unchanged. Lock does not bump it |
| `group_events (group_id, version)` | Durable roster log | Public log shape unchanged. Notify envelope gains internal audience |
| `bills.version` | Optimistic lock and bill content cursor | Used only for committed bill mutation invalidation |
| `ocr_jobs` | OCR application state | Status update and `bill_events` notify become one transaction |
| `sessions.id` as JWT `sid` | Stream ownership and revocation | No schema change. Control event names the existing `sid` |

### Internal notification contract

The shared listener registry has three fixed channels. Each module decodes and validates its own payload. Bootstrap composes handlers so one notification may feed a legacy hub and the user hub without changing the platform listener.

| Channel | Durable row | Internal routing fields | Consumers |
|---|---|---|---|
| `group_events` | `group_events` | `audience_user_ids` plus existing group event envelope | Legacy group hub and user hub |
| `bill_events` | `ocr_jobs` state, not an event log | `group_id`, `bill_id`, `audience_user_ids` | Legacy bill hub and user hub |
| `user_events` | none | Versioned envelope defined below | User hub only |

The exact expanded Group envelope is `{group_id, version, type, data, audience_user_ids}`. The exact expanded Bill envelope is `{group_id, bill_id, type, data, audience_user_ids}`. Legacy decoders ignore the added internal fields. The Bill `data` object is the public `ocr.updated` body without its duplicate `group_id` and `bill_id`; the user hub restores those two envelope values when it builds the public frame.

An invalidation uses this exact internal envelope:

```json
{
  "schema_version": 1,
  "kind": "invalidate",
  "audience_user_ids": ["uuid"],
  "body": {
    "scope": "home|group|bill|settlement",
    "group_id": "uuid",
    "resource_id": "uuid|null",
    "resource_version": "int32|null",
    "type": "string"
  }
}
```

`stream.replace` is exactly `{schema_version: 1, kind: "stream.replace", target_sids: [sid], replacement_stream_id: uuid}`. `session.ended` is exactly `{schema_version: 1, kind: "session.ended", target_sids: [sid, ...]}`. Control envelopes omit `audience_user_ids` and `body`; session ended also omits `replacement_stream_id`. Invalidation omits all control fields. Unknown schema versions, kinds, invalid combinations, nil UUIDs, and oversized payloads are rejected with bounded reason values and no raw payload log.

`audience_user_ids` is bounded by the existing limit of `50` active group members. It is deduplicated and sorted by UUID before encoding. The existing `7000` byte guard measures the complete internal envelope. If a roster notify is too large, it drops public `data` but keeps `group_id`, `version`, `type`, and audience so `/sync` can heal it. An invalidation that cannot fit is an application error and rolls back its mutation.

### State transitions

1. User stream moves through `connecting`, `ready`, `live`, and `closed`.
2. New subscribe verifies listener health, registers the new stream locally as paused without closing the old stream, and publishes `stream.replace`. Publish failure removes the new stream and returns `503` before SSE headers while the old stream stays alive. Publish success applies the control locally, lets every API process close all matching streams except `replacement_stream_id`, then writes `ready` as the first frame.
3. `session_ended`, `replaced`, `listener_reset`, `backpressure`, and `max_connection_age` write `close` when possible, then terminate the response.
4. Listener disconnect closes legacy and user subscribers. Reconnect registers all three channels before readiness becomes healthy.
5. Flutter transport moves through `signed_out`, `connecting`, `resyncing`, `live`, and `backoff`. Each registered interest separately moves through `clean`, `dirty`, and `refreshing`. Only `ready` can reset connection attempts, and only a successful REST read makes an interest clean.

### Flutter realtime interests

`RealtimeInterestRegistry` is session scoped and local to Flutter. Registration never crosses the network. It stores these exact keys:

| Interest | Key |
|---|---|
| Home groups | `home.groups` |
| Home activities | `home.activities` |
| Group index | `groups.index` |
| Settlement overview | `settlement.overview` |
| Group roster, detail, debts, activities | Surface name plus `group_id` |
| Group bill list | `group_id` plus the complete existing `GroupBillsKey`, including filters |
| Bill detail and OCR waiter | Surface name plus `group_id` and `bill_id` |

Each provider registers after its first listener mounts and unregisters with `ref.onDispose`. Dispatch builds the union of consumers from all coalesced event types, looks up matching live keys, checks `ProviderContainer.exists`, and refreshes only those instances. A newly opened provider always performs its normal initial REST load, so events missed while it was absent need no replay.

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/api/v1/users/me/events` | GET | bearer token, `X-App-Version` | User SSE frames, no JSON envelope | JWT plus live session | rollout `404` or `501`, `401 AUTHENTICATION_REQUIRED`, `429 RATE_LIMITED`, `500 STREAMING_UNSUPPORTED`, `503 REALTIME_UNAVAILABLE` |
| `/api/v1/groups/{id}` | GET | group ID | Detail, balances, `version`, caller membership | Active member | Unchanged |
| `/api/v1/groups/{id}/sync` | GET | group ID, `since` | Delta or snapshot | Active member | Unchanged |
| `/api/v1/groups/{id}/events` | GET | group ID, `since` | Legacy group SSE | Active member | Later `410 STREAM_REPLACED` |
| `/api/v1/bills/{id}` | GET | bill ID, existing group scope | Bill and current OCR summary | Active member | Unchanged |
| `/api/v1/bills/{id}/events` | GET | bill ID, existing group scope | Legacy OCR SSE | Active member | Later `410 STREAM_REPLACED` |

Successful user SSE response headers are `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`, and `X-Accel-Buffering: no`. The server checks auth, listener health, rate limit, flusher support, and successful replace publication before writing `200` or any SSE header.

`X-App-Version` uses the Flutter package form `major.minor.patch+build`, for example `1.4.0+27`. All four parts are nonnegative integers. Backend compares the four part tuple in order against `REALTIME_MIN_USER_STREAM_APP_VERSION`; missing or invalid values map to `unknown` and never qualify a legacy route for retirement.

Flutter handles connection responses with this fixed matrix:

| Result | Action |
|---|---|
| `200` plus `ready` | Stay in user mode, run mounted refetch, then process queued events |
| `401` | Refresh once and retry; refresh failure signs out |
| `404` or `501`, before any `ready` in this session | Cancel the user attempt completely, then start legacy mode |
| `429` | Stay in user mode and wait for `Retry-After` |
| `503`, timeout, or transport close | Stay in user mode and use AC-18 backoff |
| Any failure after a prior `ready` in this session | Never start legacy mode; reconnect user mode |

### Public user stream frames

User stream does not write SSE `id:` because it has no single global replay cursor. Only roster `version` participates in version fencing.

| `event:` | Required body |
|---|---|
| `ready` | `{stream_id: uuid, timestamp: RFC3339 UTC}` |
| `roster` | `{group_id: uuid, version: int64, type: string, data: object}` |
| `invalidate` | `{scope: "home"|"group"|"bill"|"settlement", group_id: uuid, resource_id?: uuid, resource_version?: int32, type: string}` |
| `ocr.updated` | `{group_id: uuid, bill_id: uuid, job_id: uuid, status: "queued"|"processing"|"succeeded"|"failed", attempts: int32, error: string|null, warnings: string[], created_at: RFC3339 UTC, updated_at: RFC3339 UTC, completed_at: RFC3339 UTC|null}` |
| `heartbeat` | `{timestamp: RFC3339 UTC}` |
| `close` | `{reason: "max_connection_age"|"session_ended"|"replaced"|"listener_reset"|"backpressure"}` |

Invalidation is a refetch hint, not a cursor. Flutter must not discard it because `resource_version` is absent, equal, or older than local state. The optional version is only useful for diagnostics and request coalescing.

### Invalidation dispatch matrix

| Event type | Public routing values | Audience | Flutter consumers |
|---|---|---|---|
| Any roster event | `roster` frame with its `group_id` and version | Audience from AC-19 | Matching roster applies delta; `homeGroupsProvider`, `homeActivitiesProvider`, and `groupsProvider` refresh |
| `bill.created`, `bill.content_changed`, `bill.reviewed` | `scope=bill`, `resource_id=bill_id`, committed bill version | All active group users | Matching bill detail, all live `groupBillsProvider` families for the group, Home groups and activities |
| `bill.deleted`, `bill.finalized`, `bill.voided` | `scope=bill`, `resource_id=bill_id`, committed or deleted row bill version | All active group users | Previous consumers plus matching group debts, group snapshot balances, and `settlementControllerProvider` |
| `group.bill_submission_locked` | `scope=group`, no resource ID or version | All active group users | Matching group header and roster snapshot, `groupsProvider`, and Home groups |
| `home.balance_changed` | `scope=home`, no resource ID or version | Debtor and creditor users | `settlementControllerProvider` and Home groups |
| `group.debts_changed` | `scope=group`, no resource ID or version | All active group users | Matching `groupDebtsProvider` and group snapshot balances |
| `bill.settlement_changed` | `scope=bill`, `resource_id=bill_id`, no version | All active group users | Matching bill detail and group bill lists |
| `settlement.payment_changed` | `scope=settlement`, `resource_id=payment_id`, no version | All active group users | `settlementControllerProvider` |
| `settlement.debt_reminded` | `scope=settlement`, `resource_id=debt_id`, no version | All active group users | `settlementControllerProvider` and matching group debts |
| `group.activity_changed` | `scope=group`, no resource ID or version | All active group users | Matching group activities and `homeActivitiesProvider` |
| `ocr.updated` | Specific OCR frame with `group_id` and `bill_id` | All active group users | Matching OCR waiter and matching bill detail |

Only mounted provider instances refresh. Family invalidation uses `group_id` and `resource_id`; it never scans a cached bill list to discover the group. Refresh calls are single flight per provider and preserve the last good state on failure.

Bill mutation type mapping is fixed as follows:

| Mutation | Invalidation type |
|---|---|
| Create bill | `bill.created` |
| Edit bill or apply OCR candidates | `bill.content_changed` |
| Review bill | `bill.reviewed` |
| Delete draft bill | `bill.deleted` |
| Finalize one bill or each successful bill in bulk finalize | `bill.finalized` |
| Void bill | `bill.voided` |

Settlement publication is fixed as follows. A row lists every invalidation emitted by that committed transaction:

| Mutation | Invalidation types | Affected bills | Audience |
|---|---|---|---|
| Create payment | `settlement.payment_changed`, `group.activity_changed` | None | All active group users |
| Submit proof | `settlement.payment_changed`, `group.debts_changed`, `bill.settlement_changed` per bill, `group.activity_changed` | Distinct bill IDs from locked payment debts | All active group users |
| Confirm payment | Previous row plus `home.balance_changed` | Distinct bill IDs from locked payment debts | Group events to all active users; Home balance only to debtor and creditor users |
| Reject payment | `settlement.payment_changed`, `group.debts_changed`, `bill.settlement_changed` per bill, `group.activity_changed` | Distinct bill IDs from locked payment debts | All active group users |
| Manual or automated debt reminder | `settlement.debt_reminded`, `group.activity_changed` | None | All active group users |
| Mark payment stalled | `settlement.payment_changed`, `group.activity_changed` | None | All active group users |

The realtime audience is independent from the existing notification target. Push or in app notification may target one counterpart while realtime invalidation targets every active member allowed to read the changed group state.

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| User subscribe | `user_id`, `sid` | Verified JWT plus live session middleware |
| User subscribe | `stream_id` | Server generated UUID v7 |
| User subscribe | Replacement winner | Commit order of the `stream.replace` PostgreSQL notification; its `replacement_stream_id` names the surviving stream |
| User subscribe | Listener admission state | Shared listener connected flag from spec 0010, checked before response headers |
| User subscribe | `Retry-After` | Remaining time in the local per SID fixed window of `10` attempts per `60` seconds, rounded up to seconds |
| `ready`, heartbeat | UTC timestamp | Server clock formatted RFC 3339 |
| Session revocation | `target_sids` | IDs returned by the session revocation `UPDATE ... RETURNING id` in the same transaction |
| Roster frame | Group, version, type, data | Committed `group_events` row, rendered by existing group delivery mapper |
| Group audience | Active users and final removed user | `group_members` read in the mutation transaction using AC-19 rules |
| Bill audience | Active users | `group_members` for the bill group read in the bill transaction |
| Bill invalidation ID and version | Bill ID and committed version | Locked `bills` row returned by the mutation |
| OCR public fields | Job state and timestamps | Committed `ocr_jobs` row returned by the status transition. Missing candidate warnings become `[]`; nonfailed error is `null`; incomplete job `completed_at` is `null` |
| Lock time | `locked_at` after refetch | `groups.bill_submission_locked_at` |
| Settlement affected bills | Distinct bill IDs | Locked debts selected by `bill_id` in the settlement transaction |
| Settlement Home audience | Debtor and creditor users | `payments` membership IDs joined to `group_members.user_id` |
| Settlement event types and consumers | Exact invalidation set | Settlement publication table and dispatch matrix in this spec |
| Home group cards | Current cards and per group balance | Existing `GET /groups` |
| Home settlement data | Payable, receivable, pending proof, and overview | Existing settlement endpoints used by `settlementControllerProvider` |
| Open group | Roster, lock, balances, bills, debts, activities | Existing group detail, `/sync`, bill list, debt list, and activity endpoints |
| Open bill | Bill and OCR summary | Existing `GET /bills/{id}` |
| Active refresh targets | Provider instance keys currently visible or alive | Session scoped `RealtimeInterestRegistry`; family providers register on creation and unregister through `ref.onDispose` |
| Request app version | `X-App-Version` | Flutter package metadata read through `package_info_plus`; unknown or missing stays `unknown` |
| Legacy cutover eligibility | Supported version threshold | `REALTIME_MIN_USER_STREAM_APP_VERSION`; unset means legacy retirement is disabled |
| Listener connection string | Direct or session mode endpoint | `DATABASE_LISTENER_URL`, falling back to `DATABASE_URL` |
| Flutter transport choice | `auto`, `legacy`, or `user` | Flutter `REALTIME_MODE` loaded into existing `EnvConfig` |
| `410` replacement | Replacement route | Static `/api/v1/users/me/events` contract |

Legacy routes use the normal API error envelope when retired:

```json
{
  "success": false,
  "error": {
    "code": "STREAM_REPLACED",
    "message": "This realtime stream has moved",
    "details": {
      "replacement": "/api/v1/users/me/events"
    }
  }
}
```

### Key invariants

1. SSE client count never changes the number of PostgreSQL listener connections.
2. Spec 0010 owns listener connection lifecycle, readiness, reconnect, cleanup, and base metrics. This spec normatively extends every fixed registry, readiness gate, semantic validation, metric allowlist, and listener test in 0010 from two channels to three by adding `user_events`. It does not add another listener connection.
3. Mutation data, audience selection, and `pg_notify` commit or roll back together for roster, bill, lock, settlement, session revocation, and OCR status. Stream replacement uses one successful autocommit notification before `ready`.
4. The user hub never derives authorization from a client supplied group ID and never stores a mutable membership cache.
5. User stream buffer overflow closes that subscriber. It never silently drops an invalidation. Legacy group hub may still drop a roster frame because `/sync` detects the version gap.
6. Listener reset closes every local subscriber before reconnect, so missed PostgreSQL notifications become a client refetch rather than silent stale state.
7. Reconnect, ready refetch, and invalidation refresh are idempotent and single flight. A failed refetch remains dirty and cannot silently become clean.
8. Roster version applies only to durable group log events. Invalidation versions never gate whether a refetch occurs.
9. At most one local user stream per `sid` survives a successful replace control. Each API process allows at most `10` connection attempts per minute per verified `sid`; excess attempts return `429` with `Retry-After`.
10. `DATABASE_LISTENER_URL` must resolve to a direct PostgreSQL endpoint or a session mode pooler. Request traffic may independently use transaction pooling through `DATABASE_URL`.

### Security model

The endpoint requires the existing live bearer session. Internal audiences come only from authenticated mutation transactions and never leave the server. A user receives a group event only when their user ID appears in that event audience, with the explicit final leave or removal exception in AC-19. Stream admission rate limiting uses the verified `sid`, never an IP address or client supplied ID.

Session replacement and revocation use server generated `stream_id` and persisted `sid`; neither value is accepted from request input. Public frames contain only roster fields already visible to members and small identifiers needed to refetch. Receipt content, signed image URLs, bank data, proof data, raw OCR, and internal audience lists never ride the stream.

### Configuration required

The feature reuses group SSE timing and adds an optional listener DSN so request pooling and session state can use compatible endpoints.

| Variable | Default | Role |
|---|---|---|
| `GROUP_SSE_HEARTBEAT_INTERVAL_SECONDS` | `15` | Heartbeat for legacy group and user stream |
| `GROUP_SSE_MAX_CONNECTION_AGE_MINUTES` | `15` | Maximum user and legacy group stream age |
| `GROUP_EVENT_RETENTION_DAYS` | `7` | Roster log retention |
| `BILL_SSE_HEARTBEAT_INTERVAL_SECONDS` | `15` | Legacy bill stream only |
| `BILL_SSE_MAX_CONNECTION_AGE_MINUTES` | `15` | Legacy bill stream only |
| `DB_APPLICATION_NAME` | `paysplit-api` | Listener and connection observability from spec 0010 |
| `DATABASE_LISTENER_URL` | `DATABASE_URL` | Dedicated listener endpoint. Production sets a direct or session mode URL when requests use transaction pooling |
| `USER_SSE_ENABLED` | `false` | Registers the user endpoint and enables `user_events` publishers. Disabled endpoint returns `404` for legacy fallback; existing `group_events` and `bill_events` publishers remain active for legacy streams |
| `REALTIME_MIN_USER_STREAM_APP_VERSION` | unset | Minimum app version eligible for legacy retirement. Unset disables `410` |
| Flutter `REALTIME_MODE` | `auto` | `auto` follows AC-23; `legacy` is rollback; `user` forbids legacy fallback |

Startup readiness requires all three `LISTEN` commands to succeed. Before production traffic is enabled, a deployment smoke test must publish one valid notification through `DATABASE_URL` and observe it through `DATABASE_LISTENER_URL`; this proves the configured path supports session state. Configuration and logs never print either database URL.

### Observability

1. Counter `paysplit_user_sse_connections_total{result}` allows `opened`, `auth_failed`, `rate_limited`, `listener_unavailable`, `streaming_unsupported`, and `replace_publish_failed`.
2. Gauge `paysplit_user_sse_active_connections` reports local active user streams.
3. Counter `paysplit_user_sse_closes_total{reason}` uses the public close reason allowlist.
4. Counter `paysplit_user_events_total{channel,kind}` allows channels `group_events`, `bill_events`, `user_events` and kinds `roster`, `invalidate`, `ocr_updated`, `stream_replace`, `session_ended`, `ready`, `heartbeat`, and `close`.
5. Counter `paysplit_user_event_invalid_payloads_total{channel,reason}` allows `invalid_json`, `unknown_schema`, `unknown_kind`, `missing_recipient`, `conflicting_recipient`, `invalid_uuid`, `invalid_body`, and `oversized`; it never logs raw payload.
6. Counter `paysplit_legacy_sse_requests_total{route,app_version_class}` allows route `group` or `bill` and version class `supported`, `legacy`, or `unknown`. The AC-23 gate requires no increase in any bucket for 30 consecutive days. Logs never contain `sid`, audience, database URLs, bank data, OCR content, or event bodies.
7. Capacity verification records active HTTP SSE connections, file descriptor use, heap per connection, listener sessions, and reconnect rate at `1000` concurrent user streams on one API instance. This is a verification target, not a production admission limit.

### Critical test scenarios

1. Connect a user stream, receive `ready`, refetch Home, then join a group from another device. The new member and existing members receive the correct roster event and Home refreshes, verifies **AC-14**, **AC-16**, **AC-19**.
2. Open the same `sid` concurrently against two API processes and force one replace publish to fail. Commit order selects one survivor, failed publish preserves the previous stream, and every successful loser closes with `replaced`, verifies **AC-14**.
3. Revoke sessions through sign in replacement, sign out, password reset, password change, refresh reuse, and admin suspend or lock. Every SID returned by the mutation closes with `session_ended`, verifies **AC-14**.
4. Attempt subscribe while the listener is unhealthy, then disconnect it while Home, group, bill, and OCR wait are mounted. New admission receives `503`; existing streams close, reconnect only after listener recovery, receive `ready`, and refetch without stale state, verifies **AC-13**, **AC-16**, **AC-18**, **AC-21**.
5. Fill the server buffer, overflow each Flutter pending limit, fail one ready refetch twice, background and resume the app. No invalidation is silently lost, dirty state retries, roster uses `/sync`, and only one transport returns, verifies **AC-15** through **AC-18**.
6. Run parallel roster mutations and force a dropped legacy roster frame. Versions remain gapless and `/sync` heals the client, verifies **AC-1** through **AC-12**.
7. Commit each bill mutation from AC-20 and roll back one forced failure. Only committed mutations emit one invalidation with the returned bill version, verifies **AC-20**.
8. Create and retry OCR, then complete it before the waiter subscribes, during a listener reset, and through failed status. `queued` is visible and initial or ready GET plus `ocr.updated` always reaches current state, verifies **AC-21**.
9. Lock bill submission, then run every settlement mutation from the matrix, including a payment covering debts from two bills. Every named provider refreshes for the exact audience, affected bill IDs are distinct, and no settlement invalidation is version filtered, verifies **AC-22**.
10. Drive every HTTP result in the AC-23 matrix, including a temporary `503` after a prior `ready`. Exactly one realtime mode is active, app version buckets stay bounded, and gated legacy routes return the exact `410`, verifies **AC-23**.
11. Connect `1000` user SSE clients, exceed the per SID admission rate once, and run the listener DSN round trip smoke test. One application `LISTEN` session covers all three channels while connection, file descriptor, heap, and reconnect measurements are recorded, verifies **AC-13**, **AC-14**.

## Build plan

The build uses a Tracer Bullet approach. Spec 0010 is the existing infrastructure prerequisite.

1. Extend every spec 0010 registry, readiness, validation, metric, and test allowlist with `user_events`. Add `DATABASE_LISTENER_URL`, healthy admission gating, listener reset closure, and the deployment round trip check while preserving one listener connection, satisfies **AC-13**, **AC-15**.
2. Add the exact channel envelopes, transaction sourced audience queries, full size validation, and user hub indexes by user, session, and stream, satisfies **AC-9**, **AC-14**, **AC-19**.
3. Add `GET /api/v1/users/me/events`, exact public frames and headers, per SID admission limit, transactional replace, every session revocation path, ready handshake, backpressure close, metrics, and OpenAPI contract, satisfies **AC-13** through **AC-16**.
4. Add `package_info_plus`, the session scoped Flutter owner, `RealtimeInterestRegistry`, parser, bounded pending state, dirty refetch retry, exact HTTP matrix, single flight reconnect, debounce, app lifecycle handling, `X-App-Version`, and authenticated shell ownership, satisfies **AC-16** through **AC-18**, **AC-23**.
5. Route roster notifications to the user hub and move group detail from legacy SSE to the shared owner while keeping `/sync`, satisfies **AC-1** through **AC-12**, **AC-19**.
6. Add transactional bill invalidations for every AC-20 mutation and wire the Home, group bill list, group debt, group snapshot, settlement, and bill detail consumers from the dispatch matrix, satisfies **AC-20**.
7. Make OCR create, retry, processing, success, and failure state plus `bill_events` notify one transaction, add complete payload fields, and move OCR wait to initial GET plus user stream recovery, satisfies **AC-21**.
8. Add transactional lock and every settlement matrix invalidation, affected bill lookup, exact realtime audiences independent from notification targets, and all matching Flutter refreshes, satisfies **AC-22**.
9. Ship user stream behind `USER_SSE_ENABLED` and Flutter `REALTIME_MODE`. Implement the exact fallback matrix and version buckets without opening both modes, satisfies **AC-23**.
10. Add multi process, transaction rollback, audience isolation, listener admission and reset, server and Flutter overflow, failed refetch, lifecycle, provider dispatch, OCR race, settlement matrix, DSN round trip, and `1000` connection coverage, satisfies **AC-13** through **AC-23**.
11. After the support and telemetry gate, return the exact `410` response, keep routes deprecated in OpenAPI for the support window, then remove legacy Flutter data sources and bill SSE timers, satisfies **AC-23**.

## Consequences

### Positive

1. Home, group, bill, and OCR share one phone connection.
2. Three PostgreSQL notification channels still use one listener session per API process.
3. Roster keeps durable catch up while other surfaces stay simple REST sources.
4. Explicit transaction audiences remove the cached membership race and make fanout authorization testable.
5. Listener loss, backpressure, and reconnect all converge on the same refetch recovery.

### Negative

1. Mutations perform a bounded active membership query and carry internal recipient IDs in the notify payload.
2. Invalidation creates extra REST reads and needs debounce under rapid bill edits.
3. Auth session revocation paths now publish a control notification.
4. The shared listener is a common failure domain, although spec 0010 already closes subscribers on failure.
5. Two public SSE modes remain during the mobile support window.

### Neutral

1. No new table, migration, vendor, broker, or PostgreSQL listener connection is introduced. One optional listener DSN separates session state from transaction pooled request traffic.
2. `GET /groups/{id}/sync` remains the only roster replay contract.
3. FCM remains responsible for waking a backgrounded app. This spec covers foreground realtime and resume recovery.

## Follow-up

1. Set and approve the product value for `REALTIME_MIN_USER_STREAM_APP_VERSION` and the retirement policy before enabling the AC-23 `410` gate.
2. Consider a durable bill event log only if measured invalidation refetch traffic becomes material.
3. Remove legacy Flutter group and bill event data sources after the support window closes.

## Migration plan

**Strategy**: Feature flagged strangler using the shared listener from spec 0010.

### Phases

1. Configure and smoke test `DATABASE_LISTENER_URL`, then deploy the expanded three channel listener, envelopes, user hub, endpoint, metrics, and transactional publishers with `USER_SSE_ENABLED=false` while legacy streams remain unchanged.
2. Enable the endpoint in development and staging. Release Flutter `REALTIME_MODE=auto` with interest registration, ready refetch, bounded recovery, and exclusive fallback. Observe fanout isolation, reconnect, REST refresh volume, capacity, and legacy route usage.
3. Make user mode the only mode in the minimum supported Flutter version. Wait for 30 consecutive days with no legacy route request in any app version bucket.
4. Return `410 STREAM_REPLACED` for legacy routes. Keep the deprecated OpenAPI entries through the support window, then remove routes, old data sources, and bill SSE configuration.

### Rollback

Set Flutter `REALTIME_MODE=legacy` and disable `USER_SSE_ENABLED`. The endpoint and `user_events` publishers stop, while expanded Group and Bill envelopes remain backward compatible with legacy hubs. `DATABASE_LISTENER_URL` may remain configured because it does not change the public protocol.

### Risks

1. A release accidentally opens user and legacy streams together. The exclusive mode owner and active connection metric detect this.
2. A publisher omits one audience. Transaction integration tests compare recipients with committed membership state for every mutation class.
3. A public invalidation is dropped under pressure. User hub closes that subscriber instead of dropping the event.
4. An old mobile binary survives past the support window. The minimum version policy must be active before legacy routes return `410`.
5. A mounted REST refresh stays unavailable while SSE is healthy. The interest remains dirty, keeps last good state, and retries independently until a successful read.
6. A transaction pooler accepts configuration but does not preserve listener session state. The pretraffic round trip smoke test blocks rollout on that endpoint.

## Rationale

Reasoning and options: see [rationale.md](rationale.md).
