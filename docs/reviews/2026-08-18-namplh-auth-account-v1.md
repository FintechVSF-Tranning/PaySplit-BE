# Review, auth and account v1, 2026-08-18

**Reviewed by**: Gemini 3.7 Flash (author on Claude / Sonnet)
**Scope**: 19 files, Auth and account v1 surface (`internal/modules/auth/`, `internal/platform/`, `internal/transport/http/`, `db/migrations/`, `docs/specs/0001-auth-account-v1/`)
**Verdict**: Changes requested

## Summary

The Auth and account v1 implementation provides a solid foundation for authentication, database backed session management, single active device enforcement, refresh token rotation with replay detection, email verification with 6-digit OTP, password recovery, profile editing, and avatar processing. The clean architecture boundaries and cryptographic handling are well designed. Two major issues require attention: email verification returns immediate success for any active account regardless of whether the submitted OTP is valid, which bypasses token verification and allows unauthenticated account state enumeration; and user token creation checks user existence using `tx.Exec` on a SELECT query, which fails to detect missing users and produces database foreign key errors instead of domain errors.

## Major

### 🟠 Email verification returns success on any OTP for active users and exposes account state, `internal/modules/auth/repository/postgres/repository.go:119-121`
**Problem**: In `VerifyEmail`, if the user row has `status == 'active'`, the function commits the transaction and immediately returns the user before checking `user_tokens` or comparing the submitted OTP hash.
**Why it matters**: Any client can send a verification request with a random or dummy OTP for any email. If the account is already active, the API responds with HTTP 200 `{"status":"active"}` without verifying credentials. This lets attackers probe whether an email is registered and active, bypassing enumeration protections. True idempotency under AC-3 requires validating that the OTP matches a previously used token for that user, rather than blindly accepting any OTP string.
**Suggested fix**: Query `user_tokens` first to find the token matching the OTP hash. If a token was already used (`used_at IS NOT NULL`) within the retention window and the account is active, return success idempotently. If the OTP does not match or is invalid, return `domain.ErrInvalidOrExpiredToken` regardless of whether the account is active.

### 🟠 User token creation uses `tx.Exec` for existence check, causing foreign key crashes on missing users, `internal/modules/auth/repository/postgres/repository.go:89-94`
**Problem**: In `CreateUserToken`, the existence check executes `tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", userID)`. In pgx, `tx.Exec` on a SELECT statement returns zero rows without an error, meaning `errors.Is(err, pgx.ErrNoRows)` is never reached.
**Why it matters**: When called with a non existent user ID, `tx.Exec` succeeds without returning `domain.ErrUserNotFound`. The subsequent `INSERT INTO user_tokens` then fails on the `user_tokens_user_id_fkey` constraint and returns an unhandled database error instead of the domain error.
**Suggested fix**: Use `tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", userID).Scan(&dummy)` so that `pgx.ErrNoRows` is correctly returned and mapped to `domain.ErrUserNotFound`.

## Minor

### 🟡 Random OTP generation uses modulo on 32-bit integer introducing slight modulo bias, `internal/modules/auth/domain/token.go:26`
**Problem**: `NewOTP` generates a 6-digit code with `(uint32(...) % 1000000)`. Because $2^{32}$ is not an exact multiple of 1,000,000, numbers from 0 to 967,295 have a very slightly higher probability of being selected.
**Why it matters**: The bias is small (around 0.02 percent), but security best practice for cryptographic credentials recommends uniform randomness without modulo bias.
**Suggested fix**: Use `crypto/rand.Int(rand.Reader, big.NewInt(1000000))` from the standard library to generate cryptographically uniform integers.

### 🟡 Redundant `tx.Exec` on SELECT queries in session revocation and password change, `internal/modules/auth/repository/postgres/repository.go:349, 432`
**Problem**: `RevokeSession` and `ChangePassword` run `tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", userID)` to lock the user record, but ignore the row scan result.
**Why it matters**: While not breaking application flow, `tx.Exec` on a SELECT does not verify whether the row actually exists before proceeding to the subsequent update statements.
**Suggested fix**: Use `QueryRow().Scan(&dummy)` if user existence is required, or rely directly on the `UPDATE users` statement with its WHERE clause.

## Nits

- ⚪ `internal/platform/security/password/bcrypt.go:34`, `len(plain)` checks byte length rather than character count; this complies with the 72-byte bcrypt limit, but multi-byte characters could satisfy the 8-byte minimum with fewer than 8 runes.

## Strengths

- Refresh token rotation atomically updates the old token and issues the replacement, revoking the entire session immediately if an already used token is replayed.
- Single active device enforcement revokes old sessions upon new sign in, and `liveAuth` middleware verifies session validity in PostgreSQL on every protected request.
- Rate limiting records events with hashed keys and advisory locks in PostgreSQL, returning accurate `Retry-After` seconds in response headers.
- Avatar handling converts images to WebP with concurrency limits, falls back to Cloudinary on unsupported formats, and reliably enqueues failed deletes to a durable retry queue.

## Test coverage

Well covered: 6-digit OTP generation, OTP validation formatting, generic forgot password responses, password change validation, single device session revocation, refresh token replay detection, bank validation, and avatar cleanup integration.

Gaps:
1. No test verifies that `VerifyEmail` with a wrong OTP is rejected when an account is already in `active` status.
2. No unit test exercises `CreateUserToken` with an unknown user ID to assert that `domain.ErrUserNotFound` is returned.
