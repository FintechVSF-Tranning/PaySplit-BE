# Review, lampt/group-module, 2026-08-17

**Reviewed by**: claude-opus-5 (author on a different model, same session)
**Scope**: 38 files, branch vs `dev` (merge base `8a6782e`)
**Verdict**: Blocked

## Summary

This is a well-built, spec-faithful implementation of group management v1 (spec 0002). The
group-row-lock-first invariant is genuinely applied in every mutating repository method, the
concurrency tests are real (two goroutines against real Postgres, not mocks), and the response DTOs
match the spec's public field lists exactly with no PII leakage. 70 tests pass under `-race` with
`TEST_DATABASE_URL` set.

Three things block or should block merge. One is a functional bug that ships a malformed invite
link with the documented default config — and the test suite asserts the malformed value as correct.
One is an authorization check that is skipped on two early-return paths in `LeaveOrRemoveMember`,
contradicting the spec's own security model. One is a migration that I reproduced failing outright
against a non-empty `groups` table.

## Blockers

### 🔴 Invite URL is built by bare string concatenation and is malformed with the shipped config, `internal/modules/group/usecase/service.go:157`

**Problem**: `InviteURL: s.inviteURLBase + result.Invite.Code`. With the value this change adds to
`.env.example` (`APP_INVITE_BASE_URL=paysplit://join`) the result is:

```
paysplit://joinsRmnxouQ3BHuu6lm_CdypDv9NTyl5JtG5BCFECJpsAI
```

There is no separator between the base and the code. The host segment of the deep link becomes
`joinsRmnxouQ...`, so the app cannot route it and cannot recover the code.

The same repo already solves this correctly for the two analogous auth deep links:
`internal/platform/email/gmail/mailer.go:67` has `tokenURL(base, token)`, which parses the base and
sets `query.Set("token", token)`, producing `paysplit://verify-email?token=…`. This module ignores
that helper and the convention behind it.

Worse, the behavior is locked in by a test — `internal/modules/group/usecase/service_test.go:358`
asserts `out.InviteURL != "paysplit://join"+"the-code"` fails, i.e. it asserts the concatenated
string is expected. The suite therefore cannot catch this, and the `/check verify` pass would only
have caught it if someone had actually opened the emitted link in the app.

**Why it matters**: `invite_url` is the shareable artifact that AC-3 exists to produce — the whole
point of the Captain invite flow. Every invite link handed to a user in the default deployment is
unusable. It is also silently wrong: the API returns 201 and a plausible-looking string.

**Suggested fix**: Build the URL through `net/url` rather than concatenation, mirroring
`gmail.tokenURL` — parse `inviteURLBase`, set the code as a query parameter (`?code=`) or append it
as a properly separated path segment, and decide which form the Flutter app expects. Validate the
base at config load so a base without a usable join point is rejected at startup. Then update
`service_test.go:358` to assert the real expected link, and add an assertion on the `invite_url`
field in the HTTP integration test.

## Major

### 🟠 `LeaveOrRemoveMember` returns success/conflict before any caller authorization, `internal/modules/group/repository/postgres/repository.go:604-615`

**Problem**: The method locks the target membership by `(id, group_id)` — which matches *any*
membership in the group, not the caller's — and then returns on two paths before authorization is
ever evaluated:

```go
target, err := q.LockMembership(...)          // any membership in the group
if fmt.Sprint(target.Status) == domain.MembershipInactive {
    return tx.Commit(ctx)                      // → 204, no caller check
}
if fmt.Sprint(target.Role) == domain.RoleCaptain {
    return domain.ErrCaptainTransferRequired   // → 409, no caller check
}
// caller identity / Captain check only begins here, line 617
```

So any authenticated user — including someone who is not and never was a member of the group —
who supplies a valid `(group_id, membership_id)` pair learns:

- `204` → that membership exists in that group and is currently inactive
- `409 CAPTAIN_TRANSFER_REQUIRED` → that membership is the group's active Captain
- `403` → anything else

This contradicts the spec directly. Security model item 5 scopes the idempotent retry to the
**membership owner**: "After exit, the membership owner may repeat the same request and receive
`204`." AC-6 likewise scopes the operation to self-leave or Captain-removal.

**Why it matters**: It is a post-revocation information leak. A user who was legitimately removed
from a group keeps both UUIDs forever and can keep polling to learn which members have since left
and who currently holds Captain — access they were specifically stripped of. The UUIDv7 identifiers
are not enumerable at scale, so this is disclosure to someone who already held the IDs rather than
a broad IDOR, and no state is mutated. That is what keeps it out of the Blocker tier, not the
absence of a real authorization gap.

**Suggested fix**: Move the caller resolution (the `target.UserID == uid` / active-Captain branch at
lines 617-632) above both early returns, so every path proves the caller is either the membership
owner or the group's active Captain before any status- or role-dependent response is produced. No
test currently covers either early-return path with an unauthorized caller; add one.

### 🟠 Migration 000002 fails against a non-empty `groups` table, `db/migrations/000002_group_management_v1.sql:9-13`

**Problem**: Reproduced on a scratch Postgres 18 database. Applying `000001`, inserting one group
with `name = '  Padded  '` and `currency = 'USD'`, then running the `000002` Up section wrapped in a
transaction (as goose does) gives:

```
ERROR:  check constraint "groups_name_check" of relation "groups" is violated by some row
```

The whole migration aborts and rolls back. Both new constraints can trigger this, and `000001`
permits exactly the rows that break them: it declares only `CHECK (name <> '')` and
`currency TEXT NOT NULL DEFAULT 'VND'` with no currency check at all, so untrimmed names and
non-VND currencies are legal today.

Secondary issues on the same statements:
- `ADD CONSTRAINT … CHECK` takes an `ACCESS EXCLUSIVE` lock and full-scans `groups` to validate. On a
  large table this blocks all traffic to it for the duration.
- `groups_currency_check` is added without a preceding `DROP CONSTRAINT IF EXISTS`, unlike
  `groups_name_check` on line 9. Re-running Up without first running Down errors on the duplicate.

**Why it matters**: `make migrate-up` fails and the release is blocked, with no data-repair step in
the migration to get past it. This fails loudly rather than corrupting data, which is why it is
Major rather than a Blocker — but if there is any existing group data in a deployed environment, the
deploy is stuck until someone hand-writes a backfill.

**Suggested fix**: Backfill first (`UPDATE groups SET name = btrim(name)`, and decide explicitly what
happens to any non-VND row), then add the constraints. For the large-table concern, add each
constraint as `NOT VALID` and follow with a separate `VALIDATE CONSTRAINT`, which takes only a
`SHARE UPDATE EXCLUSIVE` lock. Add `DROP CONSTRAINT IF EXISTS groups_currency_check` before the add,
for symmetry with line 9.

I separately verified the **Down** section is correct: the Up→Down→Up round trip succeeds, and Down
restores `000001`'s original `CHECK (name <> '')` and all four superseded indexes
(`idx_group_invites_active`, `idx_group_activities_timeline`, `idx_debts_debtor_unsettled`,
`idx_debts_creditor_unsettled`). Leaving the added enum labels in place is correctly reasoned and
documented. Also confirmed the `DO $$ … ALTER TYPE … ADD VALUE … $$` blocks do execute cleanly
inside goose's transaction on PG18.

## Minor

### 🟡 Dead `pgx.ErrNoRows` branches on every group lock, `internal/modules/group/repository/postgres/repository.go:233, 369, 473, 588`

All four mutating methods lock via `tx.Exec(ctx, "SELECT id FROM groups WHERE id=$1 FOR UPDATE", gid)`
and then test `errors.Is(err, pgx.ErrNoRows)`. `Exec` never returns `ErrNoRows` — it returns a
command tag with zero rows affected. The intended "group does not exist" mapping never fires. Each
call site happens to be rescued by the next query (`GetActiveMembership` or `LockMembership` returns
`ErrNoRows` and maps to the same domain error), so behavior is correct today by accident. Note also
that `LockGroup` is defined in `queries/groups.sql:43` and generated, but never used — using
`q.LockGroup` would make the `ErrNoRows` check real and delete the duplication.

### 🟡 `APP_INVITE_BASE_URL` validation does not validate, `internal/config/config.go:273`

`if _, err := url.Parse(c.Group.InviteBaseURL); strings.TrimSpace(...) == "" || err != nil` — but
`url.Parse` returns an error for almost nothing (it accepts `"not a url at all"`). The check is
effectively "is non-empty", while the error message promises "must be a valid HTTPS URL or deep link
base". `TestValidateAcceptsADeepLinkInviteBaseURL` only confirms the happy path. Had this check
verified the base actually forms a usable link, it would have caught the blocker above.

### 🟡 500s are returned with no logging anywhere, `internal/modules/group/delivery/http/handler.go:238-274`

The default branch of `writeDomainError` maps any unrecognized error to `500 INTERNAL_ERROR`, and
`helpers.WriteAPIError` does not log. Every wrapped repository failure (`fmt.Errorf("lock group: %w")`
and ~30 siblings) is discarded — the carefully constructed error context never reaches an operator.
This mirrors the existing auth module (`internal/modules/auth/delivery/http/handler.go:250`), so it
is a repo-wide convention rather than a regression, but this change adds ten more endpoints on top
of it. Worth logging the unmapped error with the request ID in the default branch.

### 🟡 `request_id` in the write-failure log is always empty, `internal/modules/group/delivery/http/handler.go:235`

`chiMiddleware.GetReqID(context.Background())` — a fresh background context never carries the chi
request ID, so this always logs `request_id=`. Should be `r.Context()`, which means `writeJSON` needs
the request passed in. Copied verbatim from `internal/modules/auth/delivery/http/handler.go:246`;
the duplication propagated the bug rather than fixing it.

### 🟡 `fmt.Sprint` on `interface{}` enum values is a silent authorization failure waiting to happen, throughout `repository.go`

Because sqlc cannot resolve the enum types, every enum column is `interface{}`, and the code
stringifies with `fmt.Sprint` and compares against `domain.RoleCaptain` etc. This is correct today
only because pgx returns a `string` for an OID absent from its type map. If anyone later registers
the enum types on the pool, changes the format negotiation, or sqlc starts resolving them, the
underlying value could become `[]byte` — and `fmt.Sprint` would silently yield `[99 97 112 …]`,
which compares unequal to every expected literal. The failure mode is that every Captain check
returns `403 CAPTAIN_REQUIRED` and every status check misreads, with no error raised anywhere.
Consider a small `enumString(v any) (string, error)` helper that type-switches on `string`/`[]byte`
and returns an error on anything unexpected, so a change in pgx behavior fails loudly instead of
silently denying access.

### 🟡 Captain can transfer the role to themselves, `internal/modules/group/repository/postgres/repository.go:722-767`

If `targetMembershipID` equals the caller's own membership, `bytesLess` is false, the else branch
locks the same row twice (harmless), `target.Role` is `captain` and status `active` so both guards
pass, and `DemoteToMember` followed by `PromoteToCaptain` on the same row is a no-op. The request
returns `200` with `previous_captain_member_id == current_captain_member_id` and writes a spurious
`captain_transferred` activity into the timeline. The spec says "transferring the role to **another**
active member". Reject a self-target with `400` or `404 MEMBER_NOT_FOUND`.

## Nits

- ⚪ `internal/modules/group/usecase/service.go:142`, a fresh crypto-random code is generated on every
  `CreateInvite` call even when the existing invite will be reused and the code discarded.
- ⚪ `internal/transport/http/middleware/logging_test.go:60`, `log.SetOutput(nil)` in cleanup leaves
  the global logger with a nil writer; any later `log.Printf` in this package panics. Use
  `os.Stderr`.
- ⚪ `internal/transport/http/middleware/logging_test.go:37`, the comment says the case covers "characters
  that could be mistaken for a path separator", but the input `abc-DEF_123` contains none. Either use
  an input with an encoded slash or drop the claim.
- ⚪ `internal/modules/group/repository/postgres/repository.go:720-736`, the ascending-ID membership
  lock ordering is unreachable-by-design: the exclusive `groups` row lock already guarantees no two
  transfers run concurrently, so the deadlock it guards against cannot occur. Harmless and
  defensible as defense-in-depth, but the comment presents it as load-bearing.
- ⚪ `internal/modules/group/delivery/http/handler.go:216-219`, the `20` / `1..100` limits are
  hardcoded here and again as `defaultListLimit` / `maxListLimit` in `repository.go:25-27`. Share one
  definition.

## Strengths

- The group-row-lock-first invariant is **actually** applied, not just claimed. I traced all five
  mutating methods (`CreateOrReuseInvite`, `RevokeInvite`, `RedeemInvite`, `LeaveOrRemoveMember`,
  `TransferCaptain`) and each takes the `groups` row lock as its first statement, with
  `FOR UPDATE NOWAIT` correctly reserved for the transfer path and `55P03` mapped to
  `409 CAPTAIN_TRANSFER_CONFLICT`.
- `TestRedeemInvite_*` and `TestTransferCaptain_ConcurrentRequestsProduceExactlyOneWinner` are genuine
  concurrency tests against real Postgres with real goroutines — not mocked, not simulated. That is
  the right way to test this design and it is rare to see done properly.
- AC-6 is implemented exactly as specified: `SumOpenDebtorTotal` and `SumOpenCreditorTotal` are
  separate queries, so a member whose `v_member_balances.net_balance` nets to zero still cannot
  leave. The spec called this trap out and the code avoids it.
- Response DTOs in `response.go` match the spec's public field tables field-for-field, and no
  endpoint exposes email, phone, or bank data. Avatar object keys are converted to URLs at the
  delivery boundary rather than leaking storage keys.
- `GetGroupDetail` correctly collapses "group missing" and "caller not a member" into the same
  `404 GROUP_NOT_FOUND` (invariant 12), and the comment explains *why* rather than *what*.
- The `net_balance::bigint` cast flagged for review is safe: the view sums `debts.amount`, a `BIGINT`
  with `CHECK (amount > 0)`, so the numeric source holds only integers — no precision loss, and
  overflow would require a group balance near 9.2e18 VND.

## Test coverage

70 test functions across the six new/changed test files, all passing under `go test ./... -race` with
`TEST_DATABASE_URL` pointed at the local Postgres 18. Both integration files skip cleanly when it is
unset. The distribution is sensible: 34 usecase unit tests for validation contracts, 22 repository
integration tests for transaction and authorization behavior, 6 HTTP end-to-end journeys, 5
middleware tests, 3 domain tests.

Real gaps:

- **The blocker is anti-covered.** `service_test.go:358` asserts the malformed concatenated invite
  URL as the expected value. The HTTP integration tests never assert on the `invite_url` field at
  all, so nothing in the suite exercises the link a user would actually receive.
- **The two unauthorized early-return paths in `LeaveOrRemoveMember` are untested.** There are tests
  for a non-Captain removing another member (`repository_integration_test.go:597`) and for the
  Captain-can-never-leave rule (:583), but none for a *non-member* targeting an inactive membership
  or the Captain's membership — precisely the paths that skip authorization.
- **No migration test.** Nothing exercises `000002` against a populated `groups` table, which is why
  the constraint-validation failure went unnoticed. This is arguably out of scope for a Go test
  suite, but the risk should at least be captured in the spec's verify checklist.
- `TestRedactPath_RedactsEvenWhenTheCodeLooksLikeAnotherSegment` does not test what its name and
  comment claim (see nits).
