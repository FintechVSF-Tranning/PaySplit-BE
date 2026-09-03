# 0009. Group realtime sync v1

**Date**: 2026-08-26
**Updated**: 2026-09-03
**Status**: Proposed
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

13. **AC-13**: Mỗi API process dùng shared listener của spec 0010 để `LISTEN group_events`, `bill_events`, và `user_events` trên đúng một connection khi listener healthy. Mở hoặc đóng SSE client không acquire một listener connection khác. Listener disconnect đóng mọi local SSE subscription trước khi reconnect.
14. **AC-14**: `GET /api/v1/users/me/events` yêu cầu JWT và session còn sống. Mỗi subscribe có server generated `stream_id`. Subscribe mới phát control event qua `user_events`; mọi API process đóng stream cũ có cùng `sid` với `reason=replaced`, trừ stream mang `stream_id` mới. Sign out, sign in thay thế thiết bị, password reset, và refresh reuse phát `session_ended` trong transaction thu hồi session và đóng stream bị thu hồi.
15. **AC-15**: User stream chỉ phát các frame công khai đã đóng băng trong spec này: `ready`, `roster`, `invalidate`, `ocr.updated`, `heartbeat`, và `close`. Stream không phát full bill, receipt bytes, item text, bank data, `avatar_object_key`, `audience_user_ids`, hoặc raw internal error.
16. **AC-16**: Server đăng ký local subscriber ở trạng thái paused trước khi phát replace control. `ready` luôn là public frame đầu tiên; event đến sau subscribe nhưng trước `ready` được xếp hàng. Sau mỗi `ready`, gồm initial connect và reconnect, Flutter refetch mọi mounted surface trước khi coi stream là live. Event đã xếp hàng hoặc đến trong lúc refetch được buffer và xử lý sau refetch.
17. **AC-17**: Flutter có đúng một session scoped user stream owner nằm trên authenticated navigation shell và không `autoDispose` theo page. Owner bắt đầu sau khi token của session sẵn sàng, đóng khi logout hoặc session expiry, và được tạo lại khi `sessionRevisionProvider` đổi. Page scoped providers không tự mở SSE.
18. **AC-18**: Khi stream lỗi, nhận `close`, listener reset, hoặc app resume, client dùng một reconnect single flight. Backoff là `1, 2, 4, 8, 15, 30` giây với jitter từ `70%` đến `130%`, reset chỉ sau `ready`. `max_connection_age` reconnect ngay nhưng vẫn chạy ready refetch. Các invalidation trùng trong cửa sổ `250 ms` được gộp theo `scope`, `group_id`, `resource_id`, và `type`.
19. **AC-19**: Audience được xác định trong cùng transaction với mutation và chỉ nằm trong PostgreSQL notify envelope. Join hoặc reactivate gửi cho toàn bộ active members sau mutation, gồm member mới. Leave hoặc remove gửi final roster event cho active members sau mutation cộng target user. Archive chụp active user IDs trước khi deactivate. Mọi event sau đó chỉ gửi cho audience active được mutation tương ứng xác định.
20. **AC-20**: Bill create, edit, draft delete, OCR candidate apply, review, finalize, bulk finalize success, và void gọi `pg_notify('user_events', ...)` bên trong cùng transaction đã ghi committed bill state. Payload là một `invalidate` có `group_id`, `resource_id=bill_id`, committed `resource_version`, và type cụ thể. Không có đường publish sau commit cho các mutation này.
21. **AC-21**: Mọi OCR transition `processing`, `succeeded`, hoặc `failed` ghi `ocr_jobs` và gọi `pg_notify('bill_events', ...)` trong cùng transaction. Internal bill event mang `group_id` và audience active. Public `ocr.updated` mang `group_id`, `bill_id`, `job_id`, status, attempts, nullable cleaned error, warnings, `created_at`, `updated_at`, và nullable `completed_at`. OCR waiter GET current bill and OCR summary trước khi chờ event, sau mỗi ready, và khi timeout 60 giây.
22. **AC-22**: Bill submission lock gọi `pg_notify('user_events', ...)` trong cùng transaction với `groups.bill_submission_locked_at`, không tăng `roster_version`. Settlement confirm hoặc reject lấy distinct affected `bill_id` từ các debt row đã khóa. Nó gửi `home.balance_changed` cho debtor và creditor, đồng thời gửi `group.debts_changed` và một `bill.settlement_changed` cho mỗi affected bill tới toàn bộ active group members. Settlement invalidation không có `resource_version` vì settlement không đổi `bills.version`.
23. **AC-23**: Legacy group and bill SSE routes ở lại trong giai đoạn cutover. Flutter mới thử user stream trước và chỉ fallback về legacy khi endpoint chưa tồn tại, không bao giờ mở hai mode cùng lúc. Route chỉ chuyển sang `410 STREAM_REPLACED` sau khi minimum supported app version đã dùng user stream và production telemetry không còn legacy request trong 30 ngày. Response `410` trả `replacement=/api/v1/users/me/events`; OpenAPI giữ route là deprecated cho tới khi support window kết thúc.

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
| `user_events` | none | `kind`, `audience_user_ids` or target `sid`, public `body` | User hub only |

`audience_user_ids` is bounded by the existing active group member limit. It is deduplicated and sorted by UUID before encoding. The existing `7000` byte guard measures the complete internal envelope. If a roster notify is too large, it drops public `data` but keeps `group_id`, `version`, `type`, and audience so `/sync` can heal it. An invalidation that cannot fit is an application error and rolls back its mutation.

### State transitions

1. User stream moves through `connecting`, `ready`, `live`, and `closed`.
2. New subscribe registers locally, publishes replace control, then writes `ready`.
3. `session_ended`, `replaced`, `listener_reset`, `backpressure`, and `max_connection_age` write `close` when possible, then terminate the response.
4. Listener disconnect closes legacy and user subscribers. Reconnect registers all three channels before readiness becomes healthy.
5. Flutter moves through `signed_out`, `connecting`, `resyncing`, `live`, and `backoff`. Only `ready` can reset reconnect attempts.

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/api/v1/users/me/events` | GET | bearer token | User SSE frames, no JSON envelope | JWT plus live session | `401 AUTHENTICATION_REQUIRED`, `500 STREAMING_UNSUPPORTED` |
| `/api/v1/groups/{id}` | GET | group ID | Detail, balances, `version`, caller membership | Active member | Unchanged |
| `/api/v1/groups/{id}/sync` | GET | group ID, `since` | Delta or snapshot | Active member | Unchanged |
| `/api/v1/groups/{id}/events` | GET | group ID, `since` | Legacy group SSE | Active member | Later `410 STREAM_REPLACED` |
| `/api/v1/bills/{id}` | GET | bill ID, existing group scope | Bill and current OCR summary | Active member | Unchanged |
| `/api/v1/bills/{id}/events` | GET | bill ID, existing group scope | Legacy OCR SSE | Active member | Later `410 STREAM_REPLACED` |

### Public user stream frames

User stream does not write SSE `id:` because it has no single global replay cursor. Only roster `version` participates in version fencing.

| `event:` | Required body |
|---|---|
| `ready` | `{stream_id: uuid, timestamp: RFC3339 UTC}` |
| `roster` | `{group_id: uuid, version: int64, type: string, data: object}` |
| `invalidate` | `{scope: "home"|"group"|"bill", group_id: uuid, resource_id?: uuid, resource_version?: int32, type: string}` |
| `ocr.updated` | `{group_id: uuid, bill_id: uuid, job_id: uuid, status: "processing"|"succeeded"|"failed", attempts: int32, error: string|null, warnings: string[], created_at: RFC3339 UTC, updated_at: RFC3339 UTC, completed_at: RFC3339 UTC|null}` |
| `heartbeat` | `{timestamp: RFC3339 UTC}` |
| `close` | `{reason: "max_connection_age"|"session_ended"|"replaced"|"listener_reset"|"backpressure"}` |

Invalidation is a refetch hint, not a cursor. Flutter must not discard it because `resource_version` is absent, equal, or older than local state. The optional version is only useful for diagnostics and request coalescing.

### Invalidation dispatch matrix

| Event type | Audience | Flutter consumers |
|---|---|---|
| Any roster event | Audience from AC-19 | Matching roster applies delta; `homeGroupsProvider`, `homeActivitiesProvider`, and `groupsProvider` refresh |
| `bill.created`, `bill.content_changed`, `bill.reviewed` | All active group users | Matching bill detail, all live `groupBillsProvider` families for the group, Home groups and activities |
| `bill.deleted`, `bill.finalized`, `bill.voided` | All active group users | Previous consumers plus matching group debts, group snapshot balances, and `settlementControllerProvider` |
| `group.bill_submission_locked` | All active group users | Matching group header and roster snapshot, `groupsProvider`, and Home groups |
| `home.balance_changed` | Debtor and creditor users | `settlementControllerProvider` and Home groups |
| `group.debts_changed` | All active group users | Matching `groupDebtsProvider` and group snapshot balances |
| `bill.settlement_changed` | All active group users | Matching bill detail and group bill lists |
| `ocr.updated` | All active group users | Matching OCR waiter and matching bill detail |

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

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| User subscribe | `user_id`, `sid` | Verified JWT plus live session middleware |
| User subscribe | `stream_id` | Server generated UUID v7 |
| `ready`, heartbeat | UTC timestamp | Server clock formatted RFC 3339 |
| Roster frame | Group, version, type, data | Committed `group_events` row, rendered by existing group delivery mapper |
| Group audience | Active users and final removed user | `group_members` read in the mutation transaction using AC-19 rules |
| Bill audience | Active users | `group_members` for the bill group read in the bill transaction |
| Bill invalidation ID and version | Bill ID and committed version | Locked `bills` row returned by the mutation |
| OCR public fields | Job state and timestamps | Committed `ocr_jobs` row returned by the status transition |
| Lock time | `locked_at` after refetch | `groups.bill_submission_locked_at` |
| Settlement affected bills | Distinct bill IDs | Locked debts selected by `bill_id` in the settlement transaction |
| Settlement Home audience | Debtor and creditor users | `payments` membership IDs joined to `group_members.user_id` |
| Home group cards | Current cards and per group balance | Existing `GET /groups` |
| Home settlement data | Payable, receivable, pending proof, and overview | Existing settlement endpoints used by `settlementControllerProvider` |
| Open group | Roster, lock, balances, bills, debts, activities | Existing group detail, `/sync`, bill list, debt list, and activity endpoints |
| Open bill | Bill and OCR summary | Existing `GET /bills/{id}` |
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
2. Spec 0010 owns listener connection lifecycle, readiness, reconnect, cleanup, metrics, and the direct or session mode `DATABASE_URL` requirement. This spec adds one channel to the same registry and introduces no listener DSN.
3. Mutation data, audience selection, and `pg_notify` commit or roll back together for roster, bill, lock, settlement, session control, and OCR status.
4. The user hub never derives authorization from a client supplied group ID and never stores a mutable membership cache.
5. User stream buffer overflow closes that subscriber. It never silently drops an invalidation. Legacy group hub may still drop a roster frame because `/sync` detects the version gap.
6. Listener reset closes every local subscriber before reconnect, so missed PostgreSQL notifications become a client refetch rather than silent stale state.
7. Reconnect, ready refetch, and invalidation refresh are idempotent and single flight.
8. Roster version applies only to durable group log events. Invalidation versions never gate whether a refetch occurs.

### Security model

The endpoint requires the existing live bearer session. Internal audiences come only from authenticated mutation transactions and never leave the server. A user receives a group event only when their user ID appears in that event audience, with the explicit final leave or removal exception in AC-19.

Session replacement and revocation use server generated `stream_id` and persisted `sid`; neither value is accepted from request input. Public frames contain only roster fields already visible to members and small identifiers needed to refetch. Receipt content, signed image URLs, bank data, proof data, raw OCR, and internal audience lists never ride the stream.

### Configuration required

The feature reuses listener and group SSE configuration. No new secret or database DSN is added.

| Variable | Default | Role |
|---|---|---|
| `GROUP_SSE_HEARTBEAT_INTERVAL_SECONDS` | `15` | Heartbeat for legacy group and user stream |
| `GROUP_SSE_MAX_CONNECTION_AGE_MINUTES` | `15` | Maximum user and legacy group stream age |
| `GROUP_EVENT_RETENTION_DAYS` | `7` | Roster log retention |
| `BILL_SSE_HEARTBEAT_INTERVAL_SECONDS` | `15` | Legacy bill stream only |
| `BILL_SSE_MAX_CONNECTION_AGE_MINUTES` | `15` | Legacy bill stream only |
| `DB_APPLICATION_NAME` | `paysplit-api` | Listener and connection observability from spec 0010 |

### Observability

1. Counter `paysplit_user_sse_connections_total{result}` uses a bounded result allowlist.
2. Gauge `paysplit_user_sse_active_connections` reports local active user streams.
3. Counter `paysplit_user_sse_closes_total{reason}` uses the public close reason allowlist.
4. Counter `paysplit_user_events_total{channel,kind}` uses fixed channel and kind allowlists.
5. Counter `paysplit_user_event_invalid_payloads_total{channel,reason}` never logs raw payload.
6. Legacy route requests are counted by route and normalized app version for the AC-23 cutover gate. Logs never contain `sid`, audience, bank data, OCR content, or event bodies.

### Critical test scenarios

1. Connect a user stream, receive `ready`, refetch Home, then join a group from another device. The new member and existing members receive the correct roster event and Home refreshes, verifies **AC-14**, **AC-16**, **AC-19**.
2. Open the same `sid` against two API processes. The second `stream_id` closes the first with `replaced`, verifies **AC-14**.
3. Revoke a session through sign in replacement, sign out, password reset, and refresh reuse. Every matching stream closes with `session_ended`, verifies **AC-14**.
4. Disconnect the shared listener while Home, group, bill, and OCR wait are mounted. All local streams close, reconnect, receive `ready`, and refetch without stale state, verifies **AC-13**, **AC-16**, **AC-18**, **AC-21**.
5. Fill one user subscriber buffer. Only that stream closes with `backpressure`; it reconnects and refetches instead of silently losing invalidation, verifies **AC-15**, **AC-18**.
6. Run parallel roster mutations and force a dropped legacy roster frame. Versions remain gapless and `/sync` heals the client, verifies **AC-1** through **AC-12**.
7. Commit each bill mutation from AC-20 and roll back one forced failure. Only committed mutations emit one invalidation with the returned bill version, verifies **AC-20**.
8. Complete OCR before the waiter subscribes, during a listener reset, and through failed status. Initial or ready GET plus `ocr.updated` reaches a terminal result without waiting on a lost event, verifies **AC-21**.
9. Lock bill submission and confirm a settlement covering debts from two bills. Every named provider in the matrix refreshes for the correct audience and no settlement invalidation is version filtered, verifies **AC-22**.
10. Run new Flutter with user stream available, unavailable, and closing unexpectedly. Exactly one realtime mode is active at once. After the support gate, legacy routes return the specified `410`, verifies **AC-23**.
11. Start N user SSE clients and verify one healthy application `LISTEN` session covers all three channels, verifies **AC-13**.

## Build plan

The build uses a Tracer Bullet approach. Spec 0010 is the existing infrastructure prerequisite.

1. Extend the spec 0010 listener registry with `user_events`, compose Group and Bill handlers with a new user hub, and preserve one listener connection plus listener reset closure, satisfies **AC-13**, **AC-15**.
2. Add the internal notification envelope, transaction sourced audience queries, full size validation, and user hub indexes by user, session, and stream, satisfies **AC-9**, **AC-14**, **AC-19**.
3. Add `GET /api/v1/users/me/events`, exact public frames, replace and session revocation controls, ready handshake, backpressure close, metrics, and OpenAPI contract, satisfies **AC-14** through **AC-16**.
4. Add the session scoped Flutter owner, parser, connection state, single flight reconnect, ready refetch, debounce, lifecycle handling, and authenticated shell ownership, satisfies **AC-16** through **AC-18**.
5. Route roster notifications to the user hub and move group detail from legacy SSE to the shared owner while keeping `/sync`, satisfies **AC-1** through **AC-12**, **AC-19**.
6. Add transactional bill invalidations for every AC-20 mutation and wire the Home, group bill list, group debt, group snapshot, settlement, and bill detail consumers from the dispatch matrix, satisfies **AC-20**.
7. Make OCR status update and `bill_events` notify one transaction, add complete payload fields, and move OCR wait to initial GET plus user stream recovery, satisfies **AC-21**.
8. Add transactional lock and settlement invalidations, affected bill lookup, exact audiences, and all matching Flutter refreshes, satisfies **AC-22**.
9. Ship user stream behind a Flutter realtime mode switch. Prefer user mode, fallback to legacy only when unavailable, and instrument legacy route usage without opening both modes, satisfies **AC-23**.
10. Add multi process, transaction rollback, audience isolation, listener reset, buffer overflow, provider dispatch, OCR race, and connection count coverage, satisfies **AC-13** through **AC-23**.
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

1. No new table, migration, vendor, broker, or listener DSN is introduced.
2. `GET /groups/{id}/sync` remains the only roster replay contract.
3. FCM remains responsible for waking a backgrounded app. This spec covers foreground realtime and resume recovery.

## Follow-up

1. Enroll the user stream amendment as its own scope row. `/scope` owns that file.
2. Define the product minimum supported app version and retirement policy before enabling the AC-23 `410` gate.
3. Consider a durable bill event log only if measured invalidation refetch traffic becomes material.
4. Remove legacy Flutter group and bill event data sources after the support window closes.

## Migration plan

**Strategy**: Feature flagged strangler using the shared listener from spec 0010.

### Phases

1. Deploy the new internal envelopes, user hub, third listener channel, endpoint, metrics, and transactional publishers while legacy streams remain unchanged.
2. Release Flutter user mode with initial ready refetch and exclusive fallback to legacy mode. Observe fanout isolation, reconnect, REST refresh volume, and legacy route usage.
3. Make user mode the only mode in the minimum supported Flutter version. Wait for 30 consecutive days with no supported legacy request.
4. Return `410 STREAM_REPLACED` for legacy routes. Keep the deprecated OpenAPI entries through the support window, then remove routes, old data sources, and bill SSE configuration.

### Rollback

Disable Flutter user mode and use legacy mode exclusively. The backend user endpoint and `user_events` channel may remain idle. The shared listener and internal audience fields are backward compatible with legacy hubs.

### Risks

1. A release accidentally opens user and legacy streams together. The exclusive mode owner and active connection metric detect this.
2. A publisher omits one audience. Transaction integration tests compare recipients with committed membership state for every mutation class.
3. A public invalidation is dropped under pressure. User hub closes that subscriber instead of dropping the event.
4. An old mobile binary survives past the support window. The minimum version policy must be active before legacy routes return `410`.

## Rationale

Reasoning and options: see [rationale.md](rationale.md).
