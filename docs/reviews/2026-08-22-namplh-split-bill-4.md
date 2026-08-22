# Review, namplh-split-bill-4, 2026-08-22

**Reviewed by**: gpt-5.5 (author on Codex GPT-5)
**Scope**: 47 files, uncommitted
**Verdict**: Approve

## Summary

This change implements the group governance and member invite request across schema, OpenAPI, HTTP handlers, repository transactions, and cross-module active-group gates. The main state-changing paths are well structured: group archive, invite governance, bill mutations, and settlement mutations consistently route through active membership or active group checks. I found two small HTTP contract gaps in invite creation request parsing; neither is a merge blocker, but both are worth tightening before the frontend starts depending on the exact error semantics.

## Follow-up resolution, 2026-08-22

**Final verdict**: Approve.
**Remaining findings**: None.

The author resolved both minor findings. `createInviteRequest.UnmarshalJSON` now rejects supplied top level `null` and other non object invite bodies, while `ReadOptionalJSON` still allows an omitted body. Policy values are stored as raw JSON first, so `CreateInvite` can authorize by field presence before `decodePolicy`; the new tests cover member empty body reuse, member configured and malformed policy fields returning `CAPTAIN_REQUIRED`, Captain malformed and null policy values returning 400, and top level `null` returning 400.

The mutation path remains safe. The handler does the early presence based role check only to preserve response semantics, the usecase repeats authorization before repository mutation for configured requests and verifies active membership for default requests, and the repository locks the active group then rechecks active membership and Captain role under the lock before writing. I also ran `GOCACHE=/tmp/paysplit-followup-go-cache go test ./internal/modules/group/delivery/http ./internal/modules/group/usecase`, which passed; the first default cache run failed only because the sandbox could not write to the normal Go build cache.

## Minor

### 🟡 Top-level `null` is accepted as an empty invite request, `internal/transport/http/helpers/json.go:62`

**Problem**: `ReadOptionalJSON` decodes directly into the handler DTO and treats only EOF as an omitted body. For `POST /api/v1/groups/{id}/invites`, a literal JSON `null` body decodes successfully into `createInviteRequest`, so `CreateInvite` continues as if the body had been omitted and lets any active member create or reuse the default invite.
**Why it matters**: The change request and OpenAPI contract allow either no body or a JSON object for create invite. Accepting top-level `null` silently widens the public API and makes client/server validation disagree on an edge case.
**Suggested fix**: Reject top-level JSON `null` for optional object request bodies, either in `ReadOptionalJSON` when the target is a struct or in the create-invite handler by decoding to `json.RawMessage` first. Add a request/handler test that `null` returns the same validation error as other non-object bodies.

### 🟡 Badly typed invite policy fields return 400 before the Captain-only authorization check, `internal/modules/group/delivery/http/handler.go:98`

**Problem**: `CreateInvite` parses the typed request before it asks the usecase to authorize policy configuration. Because `optionalInt` and `optionalBool` unmarshal into concrete Go types, a standard member sending a present but wrongly typed policy field such as `"regenerate": "false"` fails parsing and returns 400 from `readOptional` instead of reaching the usecase path that returns `CAPTAIN_REQUIRED`.
**Why it matters**: The group-management spec intentionally says non-Captain requests with any policy field should be rejected with 403 before policy value validation. This edge case breaks that client-facing contract and leaves the error semantics dependent on JSON value type.
**Suggested fix**: For create invite, first decode the body as a JSON object with raw fields, detect whether any policy keys are present, run the membership/role authorization decision, and only then decode and validate the policy values for Captains. Add coverage for a standard member sending malformed policy values.

## Strengths

- The new `database.LockActiveGroup` helper gives group, bill, and settlement writes a shared archive gate instead of duplicating status checks in each module.
- The disband flow is careful about preserving auditability: it deactivates members, revokes available invites, archives the group, appends activity, and leaves historical rows intact.
- The invite implementation handles several easy-to-miss cases well, including default reuse, Captain regeneration, redacted preview errors, and account plus peer-IP throttling.

## Test coverage

Coverage is strong for the main acceptance paths: create/reuse/regenerate invites, invite preview/join, active invite listing, rename, disband blocking predicates, archived group behavior, bill and settlement mutation gates, JSON helper behavior, and the fixed-window invite attempt limiter. The follow-up adds direct request DTO and HTTP integration coverage for the two parser edge cases above, so I do not see remaining coverage gaps in this focused area.
