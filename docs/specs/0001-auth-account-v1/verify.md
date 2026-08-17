# Verify 0001: Auth and account v1

Use this checklist with `/check verify auth and account v1`. It proves behavior against PostgreSQL 18 and uses fake Gmail and Cloudinary adapters unless a step explicitly says otherwise. Never put real passwords, raw tokens, phone numbers, bank numbers, or tokenized URLs in logs or committed fixtures.

## Setup

- [ ] Start PostgreSQL with `docker compose up -d postgres` and confirm it is healthy with `docker compose ps`.
- [ ] Apply the schema with `make migrate-up` and confirm Goose reports migration `000001_init_schema.up.sql` as applied.
- [ ] Set `TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/paysplit?sslmode=disable`.
- [ ] Run `go test -count=1 ./...` with a writable `GOCACHE` when the host cache is unavailable.
- [ ] Use isolated test accounts and delete them after verification.

## Acceptance criteria

- [ ] **AC-1** Sign-up rejects missing or invalid email, phone, display name, and password; accepts a Vietnamese local phone and stores E.164; duplicate normalized email and phone return their stable conflict codes.
- [ ] **AC-2** Successful sign-up returns `201`, a safe pending user, verification expiry, no auth tokens, and the actual `verification_email_sent` result. A simulated Gmail failure leaves the account and token committed.
- [ ] **AC-3** A verification token works once for ten minutes, activates the account without signing in, remains idempotent after success during retention, and an expired or superseded token fails.
- [ ] **AC-4** Resend verification and forgot password always return the same `202` body for missing, pending, and active accounts while fake-mail capture proves only eligible accounts receive mail.
- [ ] **AC-5** Sign-in accepts only email/password plus a canonical client-generated `device_id`; signing in on device B immediately revokes device A and returns one safe user plus one token pair.
- [ ] **AC-6** Five failed sign-ins inside fifteen minutes cause a fifteen-minute `429` with delta-seconds `Retry-After`; the state survives a repository restart and a later success resets the counters.
- [ ] **AC-7** The JWT contains issuer, subject, role, `sid`, and an expiry fifteen minutes after issue. A protected request rejects an expired token, revoked session, or nonactive user.
- [ ] **AC-8** Refresh values are absent from the database in plaintext, rotate atomically, expire no later than the original seven-day session, and reuse of an old token revokes the session. Concurrent refresh permits no duplicate valid branch.
- [ ] **AC-9** Sign-out returns an empty `204`; repeating it is harmless; the session, access token, and every refresh token are unusable afterward.
- [ ] **AC-10** A reset token expires after ten minutes and is single-use. Successful reset changes the password and revokes all sessions in the same transaction.
- [ ] **AC-11** Change-password rejects a wrong current password and weak new password. Success keeps only the current session and its current refresh tokens while revoking all other sessions.
- [ ] **AC-12** `GET /users/me` exposes only safe fields. Profile patch cannot change email, normalizes and uniquely validates phone, and applies or clears all bank fields as one group.
- [ ] **AC-13** Bank validation reads the embedded VietQR snapshot, accepts a `supported: true` code with a 6–19 digit account number and nonempty holder, and rejects unsupported or malformed data.
- [ ] **AC-14** Avatar upload rejects input over 10 MB and nonimages; local JPEG/PNG/GIF/WebP conversion applies EXIF orientation, strips metadata, limits the longest edge to 1024, and emits WebP quality 82. HEIC/unsupported local formats and local processing timeout fall back to Cloudinary. No fixed source-pixel cap is enforced.
- [ ] **AC-15** Avatar replacement uses a unique `paysplit/avatars/{user_id}/{uuidv7}` key and changes the database only after upload succeeds. Old-object deletion failure creates one durable retry job; delete is idempotent and clears the profile before cleanup.
- [ ] **AC-16** Resend and forgot enforce independent rolling email and IP limits of one/minute and ten/hour; sign-up enforces ten/hour/IP. Keys are hashed and events persist across process restarts.
- [ ] **AC-17** Validation, auth, rate-limit, router, and timeout responses use the nested stable `code`, `message`, and optional `fields` contract. Captured logs contain none of the forbidden secrets or PII.
- [ ] **AC-18** Cleanup retains expired, used, and revoked auth records for thirty days, deletes in batches of at most 500, and only one worker wins the PostgreSQL advisory lock. Media cleanup claims at most 50 jobs with `SKIP LOCKED`, backs off up to 24 hours, and stops automatic retries after attempt 10.
- [ ] **AC-19** `SELECT version()` reports PostgreSQL 18; all 18 UUID primary keys report `uuidv7()` defaults; expected FK, lookup, and partial unique indexes exist; application inserts omit generated IDs.

## Value sourcing

- [ ] Sign-up IDs and timestamps equal values returned by PostgreSQL defaults, not application-generated substitutes.
- [ ] Stored phone equals the request parsed with default region `VN` and normalized to E.164.
- [ ] Sign-up, reset, and change-password all use the same password-policy validator.
- [ ] Sign-up verification send flag comes from the mail adapter; its expiry equals the persisted token expiry.
- [ ] Verify-email status comes from the updated `users.status` row.
- [ ] Resend and forgot use the exact static generic message regardless of lookup result.
- [ ] Verification and reset expiries are the transaction timestamp plus ten minutes.
- [ ] Email links use the configured callback base and append only the generated token query parameter.
- [ ] Sign-in decisions use persisted user status and block fields under transaction locking.
- [ ] Session stores the required canonical installation UUID and the optional trimmed device name supplied by the client.
- [ ] JWT `sid` equals the PostgreSQL-generated session ID on sign-in and refresh.
- [ ] Access expiry is issue time plus fifteen minutes; session expiry is original sign-in time plus seven days; refresh never exceeds session expiry.
- [ ] Raw access/refresh values come from the signer and secure random generator; only the SHA-256 refresh hash is persisted.
- [ ] Sign-out and password mutation return the contractually fixed empty `204` response.
- [ ] Profile avatar URL is derived by the storage adapter from the stored Cloudinary public ID.
- [ ] Profile reads and patches select the safe row belonging to the authenticated subject and live session.
- [ ] Bank acceptance comes only from an embedded entry whose `supported` value is true.
- [ ] Uploaded avatar public ID combines the authenticated user ID with a backend-generated UUID v7 suffix and matches the provider response.
- [ ] Avatar result dimensions, quality, and size follow the uploaded bytes plus fixed AC-14 limits.
- [ ] Public error codes and messages come through the shared domain-to-HTTP mapping.

## Current automated evidence

- [x] `go vet ./...` passes.
- [x] `go test ./...` passes.
- [x] PostgreSQL repository integration covers one active device, refresh rotation, and reuse revocation.
- [x] HTTP integration covers sign-up, verification, sign-in, profile read/update, device replacement, refresh, and replay revocation.
- [x] PostgreSQL 18.6 live inspection confirms 18 of 18 UUID primary-key defaults use `uuidv7()` and the auth indexes exist.

Unchecked items intentionally remain for `/check verify`; this build step does not declare the feature complete.

## Ghi chú lần verify 2026-08-16

**Kết quả tổng thể:** `BLOCKED`. Có 16 trong 19 acceptance criteria đã được quan sát trên API thật. Không phát hiện hành vi sai, nhưng ba tiêu chí chưa có đủ bằng chứng runtime để kết luận `PASS`.

### Bằng chứng đã thu được

- API thật đã chạy với PostgreSQL 18.6 trên một database verify riêng. Goose ở version 1 và 18 trong 18 UUID primary key sử dụng `uuidv7()`.
- Đăng ký đã gửi Gmail thật và trả `verification_email_sent=true`. Khi chạy lại với SMTP cố ý không khả dụng, API vẫn trả `201`, account cùng verification token vẫn được commit và `verification_email_sent=false`.
- Verify email, resend, forgot password, reset password, change password, sign out, profile và bank validation đã được gọi qua HTTP với cả luồng thành công và lỗi chính.
- Năm lần đăng nhập sai trả lần lượt `401, 401, 401, 401, 429`. Response block có `Retry-After: 900`, và đăng nhập thành công sau khi block hết hạn đã reset bộ đếm.
- JWT có `sid`, issuer, role và TTL 900 giây. Session có lifetime 604800 giây. Refresh token chỉ tồn tại trong PostgreSQL dưới SHA-256 hash 32 byte và không vượt quá session expiry.
- Hai refresh request đồng thời trả đúng một `200` và một `401`. Reuse detection sau đó thu hồi toàn bộ session.
- Đăng nhập trên thiết bị thứ hai làm access token của thiết bị thứ nhất mất hiệu lực ngay.
- Cloudinary thật đã nhận ảnh WebP. Ảnh 1200 x 600 thành 1024 x 512. Ảnh nguồn 5200 x 5000, lớn hơn 25 triệu pixel, được chấp nhận và thành 1024 x 985.
- JPEG có EXIF orientation 6 được xoay từ 2 x 3 thành 3 x 2. File WebP tải lại không còn EXIF metadata.
- Avatar replacement tạo public ID mới. Khi replacement upload lỗi, database vẫn giữ avatar cũ. Delete và delete lặp lại đều trả `204`.
- Media cleanup worker thật đã claim và hoàn thành một cleanup job. Log quan sát được không chứa password, raw token, email, phone hoặc bank number.

### Phần còn blocked

- **AC-14:** chưa ép được local image processing timeout và chưa quan sát giới hạn concurrency trong runtime thật.
- **AC-15:** chưa tạo được Cloudinary delete failure đáng tin cậy để quan sát durable retry và backoff. Cloudinary coi destroy của object không tồn tại là một lần xóa idempotent thành công.
- **AC-18:** chưa quan sát daily auth cleanup vì interval runtime tối thiểu hiện tại là một giờ. Media cleanup success đã được quan sát, nhưng failure retry chưa có bằng chứng đầy đủ.

### Trạng thái bàn giao

- Không tick `Verify it` trong scope và không chuyển spec sang `Accepted` vì kết quả vẫn là `BLOCKED`.
- Có thể push code lên branch riêng hoặc mở Draft PR.
- Chưa nên merge vào `main` cho đến khi ba phần blocked được chấp nhận hoặc được verify bổ sung, sau đó chạy `/test auth and account v1` và `/check review auth and account v1`.
- Sau verify, toàn bộ asset Cloudinary test đã được xóa, API verify đã dừng và database verify tạm đã được xóa. PostgreSQL dự án vẫn chạy healthy.
