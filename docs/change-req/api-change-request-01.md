# API Change Request: Member Invites & Group Governance / Settings

**Date**: 2026-08-20  
**Status**: Partially resolved (updated 2026-08-21, see the per section notes below)  
**Requested by**: PaySplit FE Group Hub spec `0003-group-hub-bills-ui-v1.md`

**Update 2026-08-21**: Disband Group has moved out of this document. Its design is now the actual
source of truth at `docs/specs/0002-group-management-v1/index.md` (AC-9), reopened from
`Accepted` to `In Progress` to hold it, tracked as an unbuilt milestone in
`docs/scope/scope.md`'s "Group management v1" row. Everything else in this document (invite code
format, the new list endpoint, opening invite creation to any member, rename group) is still
exactly as proposed, not started, and not yet reconciled against spec 0002, which already locks
in different behavior for invites (Captain only creation, the current 32 byte code) that these
proposals would need to formally supersede before anyone builds them. See
`docs/specs/0002-group-management-v1/index.md`'s Follow-up item 6.

## Goal

Every active group member can share an active invite. Captain retains control of actions that can invalidate or replace an invite. One code works both as a manually entered code and as the last segment of a Deep Link, for example `https://paysplit.app/join/4AHRjDTj`.

## Invite Code Format And Deep Link

**Status (2026-08-21): not started.** The code today is still the 32 byte base64url format this
section proposes replacing (`internal/modules/group/domain/invite.go`), and spec
`0002-group-management-v1`'s Value sourcing row still locks that format in. Changing it means
updating or superseding that spec first, not just this document.

Replace the current random 32 byte Base64 URL code with a random Base62 code of exactly 8 characters. The allowed alphabet is `A-Z`, `a-z`, and `0-9`; case is significant. A code therefore matches `^[A-Za-z0-9]{8}$` and has about 218 trillion possible values.

`4AHRjDTj` is an example. The same raw code is stored in the database, entered manually, encoded in the QR, and appended to `APP_INVITE_BASE_URL`. Do not create a separate short code or a mapping table for the Deep Link.

The app receives `https://paysplit.app/join/{code}`, preserves the code through authentication when necessary, then calls `GET /api/v1/groups/invites/{code}` for preview. The user must explicitly confirm before the app calls `POST /api/v1/groups/join` with that code.

Add a database check constraint on `group_invites.code` for the exact Base62 format and retain the existing unique constraint. Update the generator and tests so collisions retry on the unique constraint violation. Update `created_by` documentation to describe the active member who created the invite, rather than only the Captain.

## App Link Hosting Note

`https://paysplit.app/join/{code}` opens the installed mobile app only after the domain association files are hosted over HTTPS. The web or infrastructure owner must publish the Apple Universal Links association file at `/.well-known/apple-app-site-association` and the Android App Links association file at `/.well-known/assetlinks.json`. Their application identifiers and signing certificate values come from the registered mobile applications and must not be guessed in this API document.

Both association files must authorize the `/join/*` path. The backend preview endpoint remains the sole server authority for the code after the mobile router receives the path. If the app is not installed, the URL serves a web fallback page. Deferred deep link support after installation is not part of this API change and needs a separate provider decision.

## Required API Changes

### List Active Invites

**Status (2026-08-21): not started.** No `GET /{id}/invites` route exists yet (confirmed against
`internal/modules/group/delivery/http/routes.go`); this is genuinely new work, not a change to
anything already built.

Add `GET /api/v1/groups/{id}/invites`.

An active group member may call this endpoint. The response returns only unrevoked, unexpired, and unexhausted invites for the group, ordered by newest first. Each item contains `id`, `code`, `invite_url`, `expires_at`, nullable `max_uses`, and `use_count`.

Return `404 GROUP_NOT_FOUND` when the group does not exist or the caller is not an active member. Do not expose inactive or historical codes.

### Create Or Reuse Invite

**Status (2026-08-21): not started.** Still Captain only today, enforced in the repository layer
(`internal/modules/group/repository/postgres/repository.go`'s `CreateOrReuseInvite`), which
directly implements spec `0002-group-management-v1`'s **AC-3** and Security model #4. This change
would reverse both, so it needs its own `/architect` pass to amend that spec, not just a code
change (a fix that only touched the usecase layer would miss the actual enforcement point).

Change `POST /api/v1/groups/{id}/invites` so every active group member may create or reuse the current active invite with an empty request body.

An active Captain may additionally provide `expires_in_hours`, `max_uses`, and `regenerate`. Only a Captain may set `regenerate: true`. A non Captain request carrying any configuration field returns `403 CAPTAIN_REQUIRED`.

The empty member request reuses the newest available invite when one exists. Otherwise it creates a default invite with the existing Backend defaults. This keeps normal member sharing idempotent and avoids duplicate active codes.

### Revoke Invite

**Status (2026-08-21): already true, no change needed.** Confirmed Captain only today
(`repository.go`'s `RevokeInvite`), matching this section exactly.

Keep `DELETE /api/v1/groups/{id}/invites/{inviteId}` restricted to the active Captain.

---

## Group Governance & Settings API Changes

### Rename Group

**Status (2026-08-21): not started.** No rename capability exists anywhere in
`internal/modules/group/` (no route, no usecase method, no query) and it has not been folded into
spec `0002-group-management-v1` yet, unlike Disband Group below. Needs its own `/architect` pass.

Add `PATCH /api/v1/groups/{id}`.

**Goal**: Cho phép Captain đổi tên nhóm chi tiêu khi có nhu cầu (ví dụ: đổi từ "Du lịch hè" thành "Du lịch Đà Lạt 2026").

- **Quyền & Xác thực**: Bearer Token + Live Session. Người gọi bắt buộc phải là **Active Captain** trong nhóm.
  - Nếu không phải Captain: Trả về `403 CAPTAIN_REQUIRED`.
  - Nếu không phải thành viên hoặc nhóm không tồn tại: Trả về `404 GROUP_NOT_FOUND`.
- **Request Body**:
  ```json
  {
    "name": "Du lịch Đà Lạt 2026"
  }
  ```
- **Validation**:
  - Áp dụng `strings.TrimSpace(name)`.
  - Độ dài hợp lệ: Từ 1 đến 100 ký tự Unicode (`utf8.RuneCountInString`).
  - Nếu tên rỗng hoặc vượt quá 100 ký tự: Trả về `400 VALIDATION_FAILED`.
- **Khóa & Giao dịch**: Khóa dòng nhóm bằng `SELECT id FROM groups WHERE id = $1 FOR UPDATE`.
- **Activity**: Ghi 1 dòng vào `group_activities` với `action_type = 'group_renamed'`, mô tả: `"{CaptainName} đã đổi tên nhóm thành \"{NewName}\""`, metadata: `{"old_name": "...", "new_name": "..."}`.
- **Success Response**: `200 OK`
  ```json
  {
    "group": {
      "id": "01912345-6789-7abc-def0-1234567890ab",
      "name": "Du lịch Đà Lạt 2026",
      "currency": "VND",
      "created_at": "2026-08-15T08:00:00Z"
    }
  }
  ```

---

### Disband / Delete Group

**Status (2026-08-21): design resolved, not yet built.** This section's two open questions are
now decided in `docs/specs/0002-group-management-v1/index.md` as **AC-9**:

1. **Soft archive, not cascade delete.** This section hedged ("Xóa mềm ... HOẶC xóa nhóm theo
   chính sách cascade"). Decided: soft archive only (`groups.status = 'archived'`, a new column),
   never delete the `groups` row or any `bills`/`debts`/`payments` history, matching how
   `bills.voided`/`debts.voided` already preserve rather than delete. This is a financial app; a
   cascade delete would destroy audit history permanently.
2. **The debts precondition below is wrong** now that `debt_status` has a `voided` value (spec
   0003, shipped after this document was written): `status <> 'settled'` would wrongly count a
   voided (cancelled) debt as still blocking disbandment. The corrected predicate is
   `status NOT IN ('settled', 'voided')`. The exact same bug was found live in the already
   shipped member exit check (spec 0002 AC-6) and fixed via `/debug` on 2026-08-21
   (`db/migrations/000008_group_exit_voided_debt_fix.sql`); Disband Group must use the corrected
   form from the start rather than repeat it.
3. **The bills precondition below is also too narrow.** `status = 'draft'` alone misses a bill
   that is `reviewed` but not yet `finalized` (a status spec 0003 added). Corrected:
   `status NOT IN ('finalized', 'voided')`.

None of this is built yet, it is design only; tracked as an unbuilt milestone under "Group
management v1" in `docs/scope/scope.md`. The corrected precondition queries, the enforcement
mechanism for write access on an archived group, and the full transaction contract are specified
in spec 0002's AC-9, Key invariant 13, Security model 4a, and Transaction and concurrency
contract, superseding the sketch below; read the spec before implementing this section, not just
this document.

Add `DELETE /api/v1/groups/{id}`.

**Goal**: Cho phép Captain giải tán và xóa nhóm khi nhóm đã hoàn thành mục đích chi tiêu và không còn bất kỳ nghĩa vụ tài chính nào.

- **Quyền & Xác thực**: Bearer Token + Live Session. Người gọi bắt buộc phải là **Active Captain** trong nhóm.
  - Nếu không phải Captain: Trả về `403 CAPTAIN_REQUIRED`.
  - Nếu không phải thành viên hoặc nhóm không tồn tại: Trả về `404 GROUP_NOT_FOUND`.
- **Điều kiện tiên quyết (Pre-conditions & Invariants)**:
  - Tất cả các hóa đơn trong nhóm phải ở trạng thái kết thúc (`finalized` hoặc `voided`), không còn hóa đơn nháp hoặc đã review nhưng chưa chốt (`draft`, `processing` OCR, hoặc `reviewed`):
    `SELECT count(*) FROM bills WHERE group_id = $1 AND status NOT IN ('finalized', 'voided')` phải bằng `0`. (Sửa 2026-08-21: bản gốc chỉ check `status = 'draft'`, bỏ sót trạng thái `reviewed`.)
  - Toàn bộ công nợ giữa tất cả các thành viên trong nhóm phải được tất toán sạch 100% (không còn bất kỳ dòng nợ nào có trạng thái khác `settled` hoặc `voided`):
    `SELECT count(*) FROM debts WHERE group_id = $1 AND status NOT IN ('settled', 'voided')` phải bằng `0`. (Sửa 2026-08-21: bản gốc chỉ check `status <> 'settled'`, coi nợ đã `voided` là vẫn còn treo, sai kể từ khi spec 0003 thêm giá trị `voided`.)
  - Nếu còn hóa đơn chưa chốt hoặc còn nợ chưa tất toán: Trả về `409 GROUP_HAS_UNSETTLED_OBLIGATIONS` kèm chi tiết số lượng hóa đơn/công nợ tồn đọng.
- **Hành động**:
  - Đánh dấu trạng thái tất cả các thành viên active thành `inactive` (`left_at = now()`).
  - Lưu trữ mềm nhóm (`UPDATE groups SET status = 'archived'`). Quyết định 2026-08-21: không xóa cascade, xem ghi chú Status ở trên.
  - Thu hồi tất cả các mã mời active còn lại của nhóm.
- **Success Response**: `204 No Content`.

---

## Security And Audit Requirements

**Status (2026-08-21) on rate limiting: partially achievable today, needs a decision.** The
existing middleware (`internal/transport/http/middleware/ratelimit.go`) is IP only, in memory,
and single instance; it has no per account dimension and is not currently wired onto any group
routes. "Rate limit by account and IP" as stated cannot be built by reusing it as is, this needs
its own small design decision (extend the middleware with a user ID dimension, or accept IP only
for v1) before implementation, not a silent choice made in code.

Invite codes are sensitive. Return a raw code only from the list and create or reuse responses to active members of that group. Redact codes and invite URLs from HTTP access logs, application logs, activity metadata, analytics, and error reports. Rate limit preview and join attempts by account and IP address, and return the same public error shape for an unknown, expired, revoked, or exhausted code.

Write an `invite_created` activity when a new invite is created (**status: already exists**, `activity_type` has carried this value since `000002_group_management_v1.sql`; no schema change needed). Reusing an existing invite does not write an activity. Include the invite ID and policy fields in activity metadata, never the code or URL.

Write a `group_renamed` activity when group name is updated by the Captain (**status: not started**, `group_renamed` does not exist in `activity_type` yet, would need a migration alongside the Rename Group work above). The parallel `group_archived` activity for Disband Group is already specified in spec 0002's Activity contract.

## Frontend Dependency

`PaySplit-FE/docs/specs/0003-group-hub-bills-ui-v1.md` requires these APIs for:
1. Member Invite Code Bar and Management Modal Sheet.
2. Group Settings Modal Bottom Sheet (`GroupSettingsBottomSheet`): Rename group, Member roles & governance, Leave group, and Disband group.

