# Profile and avatar

## Summary

This slice gives the mobile app a safe current user profile, validated bank details, and Cloudinary backed avatars. Images become WebP before storage when the backend can decode them, with Cloudinary as the fallback for formats such as HEIC.

## Requirements

This child implements **AC-12** through **AC-15** and the profile parts of **AC-17** from [index.md](index.md).

## Decision

Use an embedded snapshot from `https://vietqr.app/banks.json` for bank codes. Use `github.com/deepteams/webp` for backend WebP encoding and `github.com/cloudinary/cloudinary-go/v2` behind an `AvatarStorage` interface. Store a unique Cloudinary `public_id` in `avatar_object_key`. (basis: the confirmed backend first conversion rule and Cloudinary incoming transformation support)

Short rationale: a committed bank snapshot removes a runtime dependency from profile updates. Backend conversion satisfies the canonical WebP requirement for common formats, while Cloudinary fallback avoids native HEIC libraries in Alpine.

## Feature design

### Profile API

`GET /api/v1/users/me` returns the authenticated user's safe fields and a derived avatar URL. It never returns password hash, token data, Cloudinary credentials, or internal failure counters.

`PATCH /api/v1/users/me` accepts display name, phone number, and the three default bank fields. Email is immutable in v1. Display name is 1 to 100 Unicode characters after trim. Normalize phone with `github.com/nyaruka/phonenumbers`, default region `VN`, enforce a maximum of 16 characters including `+`, then enforce uniqueness.

Bank code, account number, and holder form one group. Omitted fields are unchanged. Three explicit null values clear the group. Any partial or invalid group rejects the whole patch. Accept only a bank entry with `supported: true`, 6 to 19 ASCII account digits, and a holder of 1 to 100 Unicode characters after trim.

### Bank snapshot

Commit the fetched JSON under a platform data package and embed it into the binary. Parse and validate it at startup. Startup fails loudly if the file is malformed, empty, has duplicate codes, or contains no supported banks. Updating the list is a reviewed repository change.

### Avatar upload

`PUT /api/v1/users/me/avatar` accepts one multipart field named `avatar` and rejects input over 10 MB before full buffering. Detect content from bytes, not filename. Apply EXIF orientation, strip metadata, resize the longest edge to at most 1024 pixels, and encode WebP at quality 82.

The backend attempts local decoding and conversion first. JPEG, PNG, GIF, and WebP use local conversion when supported. If decoding or conversion is unsupported, including common HEIC input, send the original to Cloudinary with a signed upload and concrete `format=webp`. Do not use `f_auto` for stored normalization.

Use a new public ID `paysplit/avatars/{user_id}/{uuidv7}` for every upload. The backend generates this UUID v7 as a storage key, not as a database primary key. Never overwrite the current asset. The unique URL prevents a newly uploaded avatar from reusing an old CDN cache entry. Upload timeout is 15 seconds with no automatic retry. Only after Cloudinary returns a WebP asset does the repository update `avatar_object_key`. Then attempt to destroy the old asset with CDN invalidation. Old cleanup failure does not fail the request and creates a durable `media_cleanup_jobs` row.

`DELETE /api/v1/users/me/avatar` clears `avatar_object_key` first and returns `204` even when already empty. Cloudinary deletion runs afterward. A cleanup failure creates a durable media cleanup job but never restores the profile reference.

### Media cleanup queue

`media_cleanup_jobs` stores provider, object key, attempt count, next attempt time, redacted last error code, completion time, and timestamps. A partial unique index prevents two open jobs for the same provider and object key. A worker runs every minute, claims at most 50 due rows with `FOR UPDATE SKIP LOCKED`, and uses exponential backoff capped at 24 hours. It stops automatic retry after 10 attempts, retains the failed row for manual inspection, and emits a redacted operational event. Completed jobs follow the 30 day cleanup retention.

### Resource safety

Limit multipart parsing to 10 MB, backend conversion to 10 seconds, local conversions to two concurrent tasks by default, and Cloudinary response size to the fields required by the adapter. V1 deliberately has no fixed source pixel count limit. Reject malformed images and invalid dimensions as `400 INVALID_IMAGE`. Return `413 PAYLOAD_TOO_LARGE` for input size and `502 IMAGE_STORAGE_FAILED` for storage failure while preserving the old avatar. The missing source pixel ceiling is an accepted prototype resource risk and should be reconsidered before production.

### Request and response contract

Profile JSON bodies are limited to 64 KB and reject unknown fields. Avatar multipart requests accept exactly one `avatar` part and reject unexpected file parts. Success bodies and the canonical safe user object are fixed in [index.md](index.md). Avatar URLs derive from the unique stored public ID and contain no signed secret.

## Critical test scenarios

1. Profile returns only safe fields and a URL derived from the stored public ID.
2. Phone normalization catches equivalent duplicate Vietnamese numbers.
3. Bank group update is atomic and only accepts supported snapshot entries.
4. JPEG and PNG become bounded WebP locally with metadata removed.
5. HEIC fallback asks Cloudinary to store WebP and updates the profile only after success.
6. Timeout preserves the old avatar, replacement uses a distinct public ID, and deletion remains idempotent when cleanup enters the durable retry queue.
7. Cleanup jobs are deduplicated, claimed safely by concurrent workers, stop after 10 failures, and retain no sensitive provider error body.
