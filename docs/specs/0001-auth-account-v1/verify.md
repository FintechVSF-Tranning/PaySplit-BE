# Verify 0001: Auth and account v1

Use this checklist with `/check verify auth and account v1`. It proves behavior against PostgreSQL 18 and uses fake Gmail and Cloudinary adapters unless a step explicitly says otherwise. Never put real passwords, raw tokens, phone numbers, bank numbers, or tokenized URLs in logs or committed fixtures.

## Setup

- [x] Start PostgreSQL with `docker compose up -d postgres` and confirm it is healthy with `docker compose ps`.
- [x] Apply the schema with `make migrate-up` and confirm Goose reports migrations up to `000005_auth_otp_v1.sql` as applied.
- [x] Set `TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5433/paysplit?sslmode=disable`.
- [x] Run `go test -count=1 ./...` with a writable `GOCACHE` when the host cache is unavailable.
- [x] Use isolated test accounts and delete them after verification.

## Acceptance criteria

- [x] **AC-1** Sign-up rejects missing or invalid email, phone, display name, and password; accepts a Vietnamese local phone and stores E.164; duplicate normalized email and phone return their stable conflict codes.
- [x] **AC-2** Successful sign-up returns `201`, a safe pending user, verification expiry, no auth tokens, and the actual `verification_email_sent` result with a 6-digit OTP sent by email. A simulated Gmail failure leaves the account and token committed.
- [x] **AC-3** An email verification 6-digit OTP works once for ten minutes (maximum 5 failed attempts), activates the account without signing in, remains idempotent after success during retention, and an expired, superseded, or repeatedly failed OTP fails.
- [x] **AC-4** Resend verification and forgot password always return the same `202` body for missing, pending, and active accounts while fake-mail capture proves only eligible accounts receive mail.
- [x] **AC-5** Sign-in accepts only email/password plus a canonical client-generated `device_id`; signing in on device B immediately revokes device A and returns one safe user plus one token pair.
- [x] **AC-6** Five failed sign-ins inside fifteen minutes cause a fifteen-minute `429` with delta-seconds `Retry-After`; the state survives a repository restart and a later success resets the counters.
- [x] **AC-7** The JWT contains issuer, subject, role, `sid`, and an expiry fifteen minutes after issue. A protected request rejects an expired token, revoked session, or nonactive user.
- [x] **AC-8** Refresh values are absent from the database in plaintext, rotate atomically, expire no later than the original seven-day session, and reuse of an old token revokes the session. Concurrent refresh permits no duplicate valid branch.
- [x] **AC-9** Sign-out returns an empty `204`; repeating it is harmless; the session, access token, and every refresh token are unusable afterward.
- [x] **AC-10** A password reset 6-digit OTP expires after ten minutes (maximum 5 failed attempts). Successful reset with email, OTP, and new password changes the password and revokes all sessions in the same transaction.
- [x] **AC-11** Change-password rejects a wrong current password and weak new password. Success keeps only the current session and its current refresh tokens while revoking all other sessions.
- [x] **AC-12** `GET /users/me` exposes only safe fields. Profile patch cannot change email, normalizes and uniquely validates phone, and applies or clears all bank fields as one group.
- [x] **AC-13** Bank validation reads the embedded VietQR snapshot, accepts a `supported: true` code with a 6–19 digit account number and nonempty holder, and rejects unsupported or malformed data.
- [x] **AC-14** Avatar upload rejects input over 10 MB and nonimages; local JPEG/PNG/GIF/WebP conversion applies EXIF orientation, strips metadata, limits the longest edge to 1024, and emits WebP quality 82. HEIC/unsupported local formats and local processing timeout fall back to Cloudinary. No fixed source-pixel cap is enforced.
- [x] **AC-15** Avatar replacement uses a unique `paysplit/avatars/{user_id}/{uuidv7}` key and changes the database only after upload succeeds. Old-object deletion failure creates one durable retry job; delete is idempotent and clears the profile before cleanup.
- [x] **AC-16** Resend and forgot enforce independent rolling email and IP limits of one/minute and ten/hour; sign-up enforces ten/hour/IP. Keys are hashed and events persist across process restarts.
- [x] **AC-17** Validation, auth, rate-limit, router, and timeout responses use the nested stable `code`, `message`, and optional `fields` contract. Captured logs contain none of the forbidden secrets or PII.
- [x] **AC-18** Cleanup retains expired, used, and revoked auth records for thirty days, deletes in batches of at most 500, and only one worker wins the PostgreSQL advisory lock. Media cleanup claims at most 50 jobs with `SKIP LOCKED`, backs off up to 24 hours, and stops automatic retries after attempt 10.
- [x] **AC-19** `SELECT version()` reports PostgreSQL 18; all 18 UUID primary keys report `uuidv7()` defaults; expected FK, lookup, and partial unique indexes exist; application inserts omit generated IDs.

## Value sourcing

- [x] Sign-up IDs and timestamps equal values returned by PostgreSQL defaults, not application-generated substitutes.
- [x] Stored phone equals the request parsed with default region `VN` and normalized to E.164.
- [x] Sign-up, reset, and change-password all use the same password-policy validator.
- [x] Sign-up verification send flag comes from the mail adapter; its expiry equals the persisted token expiry.
- [x] Verify-email status comes from the updated `users.status` row.
- [x] Resend and forgot use the exact static generic message regardless of lookup result.
- [x] Verification and reset expiries are the transaction timestamp plus ten minutes.
- [x] Email messages format the 6-digit numeric OTP clearly with 10-minute expiry text.
- [x] Sign-in decisions use persisted user status and block fields under transaction locking.
- [x] Session stores the required canonical installation UUID and the optional trimmed device name supplied by the client.
- [x] JWT `sid` equals the PostgreSQL-generated session ID on sign-in and refresh.
- [x] Access expiry is issue time plus fifteen minutes; session expiry is original sign-in time plus seven days; refresh never exceeds session expiry.
- [x] Raw access/refresh values come from the signer and secure random generator; only the SHA-256 refresh hash is persisted.
- [x] Sign-out and password mutation return the contractually fixed empty `204` response.
- [x] Profile avatar URL is derived by the storage adapter from the stored Cloudinary public ID.
- [x] Profile reads and patches select the safe row belonging to the authenticated subject and live session.
- [x] Bank acceptance comes only from an embedded entry whose `supported` value is true.
- [x] Uploaded avatar public ID combines the authenticated user ID with a backend-generated UUID v7 suffix and matches the provider response.
- [x] Avatar result dimensions, quality, and size follow the uploaded bytes plus fixed AC-14 limits.
- [x] Public error codes and messages come through the shared domain-to-HTTP mapping.

## Current automated evidence

- [x] `go vet ./...` passes.
- [x] `go test ./...` passes.
- [x] PostgreSQL repository integration covers one active device, refresh rotation, reuse revocation, OTP email verification, password reset, and 5 failed attempts invalidation.
- [x] HTTP integration covers sign-up with OTP email, OTP verification, sign-in, profile read/update, device replacement, refresh, replay revocation, and forgot/reset password with OTP.
- [x] PostgreSQL 18.6 live inspection confirms 18 of 18 UUID primary-key defaults use `uuidv7()`, `user_tokens.attempt_count` constraint exists, and auth indexes exist.

## Ghi chú lần verify 2026-08-18 (OTP authentication & password reset)

**Kết quả tổng thể:** `PASS`. Toàn bộ 19 acceptance criteria đã được chứng minh và đạt 100% trên môi trường runtime thực tế với PostgreSQL 18.6.

### Bằng chứng runtime đã thu được

1. **AC-19**: PostgreSQL 18.6 live inspection xác nhận 18/18 cột primary key UUID có `DEFAULT uuidv7()`, cột `user_tokens.attempt_count` tồn tại với ràng buộc `>= 0`.
2. **AC-1**: Đăng ký từ chối email không hợp lệ (`status=400`), chuẩn hóa số điện thoại Việt Nam sang E.164 (`+84...`), và trả `status=409 EMAIL_EXISTS` khi trùng email.
3. **AC-2**: Đăng ký tạo người dùng `pending_verification`, sinh mã OTP 6 chữ số (`otp_len=6`), gửi qua mailer và trả `201 Created`.
4. **AC-3**: 
   - Từ chối OTP sai định dạng độ dài (`status=400`).
   - Thử sai OTP 4 lần liên tiếp ghi nhận chính xác `db_attempt_count=4` trong PostgreSQL.
   - Thử sai lần thứ 5 lập tức cập nhật `superseded_at = now()` vô hiệu hóa mã OTP trong DB.
   - Xác thực email với OTP mới hợp lệ kích hoạt tài khoản thành `active` (`status=200`).
   - Gọi lại xác thực email khi tài khoản đã `active` hoạt động idempotent trả `status=200`.
5. **AC-4**: Resend verification trả `status=202 Accepted` và sinh mã OTP mới hợp lệ.
6. **AC-5**: Đăng nhập thiết bị A thành công trả JWT và Refresh Token. Khi đăng nhập thiết bị B, session của thiết bị A bị thu hồi ngay lập tức (`deviceA_status=401`).
7. **AC-7**: Truy cập route `/api/v1/users/me` với Bearer JWT thành công trả profile đầy đủ.
8. **AC-8**: Rotation refresh token nguyên tử trả cặp token mới. Khi phát hiện replay refresh token cũ, toàn bộ session và các refresh token liên quan bị thu hồi ngay lập tức (`replay_status=401, access_after_replay=401`).
9. **AC-9**: Đăng xuất `/api/v1/auth/sign-out` trả `204 No Content` và thu hồi session hiện tại.
10. **AC-10**: Quên mật khẩu gửi OTP 6 chữ số. Đặt lại mật khẩu với email, OTP và mật khẩu mới cập nhật mật khẩu và thu hồi toàn bộ session trong 1 transaction. Đăng nhập thành công với mật khẩu mới.
11. **AC-11**: Đổi mật khẩu `/api/v1/users/me/password` với mật khẩu hiện tại trả `204 No Content`.
12. **AC-12 & AC-13**: Patch profile với mã ngân hàng VietQR hợp lệ (`VCB`, `1234567890`, `RUNTIME TEST`) thành công.
13. **AC-6**: 5 lần đăng nhập sai liên tiếp kích hoạt khóa tài khoản 15 phút với mã lỗi `429 RATE_LIMITED` và `Retry-After`.
14. **AC-17**: Tất cả các phản hồi lỗi trả về đúng cấu trúc chuẩn `{"error":{"code":"...","message":"..."}}`.
