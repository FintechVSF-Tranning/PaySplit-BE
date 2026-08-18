# Email and password recovery

## Summary

This slice activates accounts and recovers passwords through short lived single use six digit numeric OTP codes sent by email. Gmail SMTP is an adapter, not a dependency of the auth usecase, and delivery failure never keeps a database transaction open.

## Requirements

This child implements **AC-2** through **AC-4**, **AC-10**, **AC-11**, **AC-16**, **AC-17**, and the email token part of **AC-18** from [index.md](index.md).

## Decision

Use `github.com/wneessen/go-mail` with Gmail SMTP, TLS on port 587, and a Google App Password. The usecase depends on a `Mailer` interface. Verification and password reset OTP codes share the `user_tokens` table, distinguished by `type` enum (`email_verification`, `password_reset`). OTP codes are stored as SHA 256 bytea hashes with an `attempt_count` tracking failed guesses. (basis: the personal Gmail sender requirement, Google App Password guidance, and six digit numeric OTP UX)

Short rationale: Six digit numeric OTP codes provide a mobile friendly verification experience compared to long link tokens. Storing the SHA 256 hash alongside an attempt counter prevents brute force search across the 1,000,000 code space while keeping database lookups fast and secure.

## Feature design

### OTP lifecycle and security

Generate a six digit numeric code (`000000` to `999999`) using cryptographically secure randomness from `crypto/rand`. Persist only the SHA 256 hash (`token_hash BYTEA`, 32 bytes) in `user_tokens`.

- **Expiry**: OTP codes expire after 10 minutes.
- **Brute force protection**: Each token tracks `attempt_count`. A failed verification increments `attempt_count`. When `attempt_count` reaches 5, the token is superseded immediately (`superseded_at = now()`), blocking further guesses on that code.
- **Single active OTP per type**: Creating a new OTP locks the user row, marks every unused and unsuperseded token of the same user and type as superseded (`superseded_at = now()`), then inserts the replacement in the same transaction. A partial unique index on `(user_id, type)` where both `used_at IS NULL` and `superseded_at IS NULL` guarantees at most one active OTP per type.
- **Terminal states**: `used_at` means the OTP completed its intended action. `superseded_at` means a newer OTP replaced it or it was invalidated due to reaching the maximum attempt limit.

### API details

#### `POST /api/v1/auth/verify-email`

Accept `email` and `otp` (6 digits string). Normalize email and find the target user. If the user is already `active`, return `200` with `status: "active"` idempotently.

Lock the active `email_verification` token row for the user.
- If no active token exists or the token is expired: return `400 INVALID_OR_EXPIRED_TOKEN`.
- If the token hash does not match the SHA 256 of the supplied OTP: increment `attempt_count`. If `attempt_count >= 5`, set `superseded_at = now()`. Return `400 INVALID_OR_EXPIRED_TOKEN`.
- If the token hash matches and the token is valid: set `email_verified_at = now()`, change user status from `pending_verification` to `active`, and set `used_at = now()` in one database transaction. Return `200` with `status: "active"`. No auth tokens are issued.

#### `POST /api/v1/auth/resend-verification`

Accept `email` and always return the same `202` body (`message: "If the account is eligible, an email will be sent."`). Only a `pending_verification` account receives a new OTP email. Invalidate earlier verification tokens before creating the new OTP. Limit normalized email and IP as independent dimensions to one request per rolling minute and 10 per rolling hour. Exceeding either limit rejects the request with `429 RATE_LIMITED`.

#### `POST /api/v1/auth/forgot-password`

Accept `email` and always return the same `202` body. Only an existing active account receives an OTP email. Invalidate earlier reset tokens before creating the new OTP. Use the same independent persisted limits as resend verification (1 per minute, 10 per hour).

#### `POST /api/v1/auth/reset-password`

Accept `email`, `otp` (6 digits string), and `new_password`. Validate the shared password policy (8 to 72 bytes, uppercase, lowercase, digit).

Find the user by normalized email and lock the active `password_reset` token row.
- If no active token exists or the token is expired: return `400 INVALID_OR_EXPIRED_TOKEN`.
- If the token hash does not match the SHA 256 of the supplied OTP: increment `attempt_count`. If `attempt_count >= 5`, set `superseded_at = now()`. Return `400 INVALID_OR_EXPIRED_TOKEN`.
- If the token hash matches and the token is valid: update the bcrypt password hash on `users`, set `used_at = now()` on the token, and revoke all active sessions (`revoked_at = now()`, `revoked_reason = 'password_reset'`) and their refresh tokens in one transaction. Return `204`.

#### `PUT /api/v1/users/me/password`

Require a bearer token and live session. Accept `current_password` and `new_password`. Require the current password to match, the new password to differ, and the shared policy to pass. Update the hash and revoke every session except the current `sid` in one transaction. Keep the current session and its existing refresh tokens. Return `204`.

### Rate limit persistence

Store accepted request events in `auth_rate_limit_events`. Use `action`, `dimension`, and a SHA 256 hash of the normalized email or canonical IP as the key. Email and IP are separate keys, not a combined pair. Acquire transaction scoped advisory locks for all applicable keys in deterministic order, count events inside the rolling minute and hour, then insert the accepted event. Sign up records only the IP dimension. Daily auth cleanup removes events older than 24 hours in bounded batches.

### Delivery failure

Gmail settings are required and validated at process startup. Commit the user and token before SMTP. Send with a 5 second timeout. If configured Gmail is unavailable at request time or delivery fails, sign up returns `201` with `verification_email_sent: false`. Resend and forgot password retain their generic `202`. Log a redacted event and do not automatically retry in v1.

### Email templates

Provide plain text and HTML templates for verification and reset. Templates receive the display name, the 6 digit numeric OTP formatted clearly, and 10 minute expiry text. HTML escapes all values.

## Configuration

Use the SMTP variables in [index.md](index.md). `.env.example` and README explain that Gmail requires two step verification and an App Password. The normal Gmail password must never be accepted as configuration.

### Request and response contract

JSON bodies are limited to 64 KB and reject unknown fields. Email is at most 254 bytes after normalization. OTP input is a 6 digit numeric string. New password is 8 to 72 bytes. Rate limited responses use `Retry-After` as delta seconds.

## Critical test scenarios

1. Sign up generates a 6 digit OTP, commits the account, and sends an email when SMTP is healthy.
2. Email verification succeeds with correct OTP, activates the account, and is idempotent on repeat calls.
3. Email verification rejects incorrect OTP, increments `attempt_count`, and supersedes the token after 5 failed attempts.
4. Forgot password does not reveal whether an email exists, delivering a 6 digit OTP only to existing active accounts.
5. Password reset with valid email, OTP, and new password updates credentials and revokes all active sessions.
6. Password reset with invalid or expired OTP fails and does not modify password or revoke sessions.
7. Rate limiting enforces rolling minute and hour windows on both email and IP dimensions.
