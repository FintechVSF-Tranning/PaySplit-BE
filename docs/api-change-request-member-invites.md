# API Change Request, Member Invites

**Date**: 2026-08-19  
**Status**: Proposed  
**Requested by**: PaySplit FE Group Hub spec `0003-group-hub-bills-ui-v1.md`

## Goal

Every active group member can share an active invite. Captain retains control of actions that can invalidate or replace an invite. One code works both as a manually entered code and as the last segment of a Deep Link, for example `https://paysplit.app/join/4AHRjDTj`.

## Invite Code Format And Deep Link

Replace the current random 32 byte Base64 URL code with a random Base62 code of exactly 8 characters. The allowed alphabet is `A-Z`, `a-z`, and `0-9`; case is significant. A code therefore matches `^[A-Za-z0-9]{8}$` and has about 218 trillion possible values.

`4AHRjDTj` is an example. The same raw code is stored in the database, entered manually, encoded in the QR, and appended to `APP_INVITE_BASE_URL`. Do not create a separate short code or a mapping table for the Deep Link.

The app receives `https://paysplit.app/join/{code}`, preserves the code through authentication when necessary, then calls `GET /api/v1/groups/invites/{code}` for preview. The user must explicitly confirm before the app calls `POST /api/v1/groups/join` with that code.

Add a database check constraint on `group_invites.code` for the exact Base62 format and retain the existing unique constraint. Update the generator and tests so collisions retry on the unique constraint violation. Update `created_by` documentation to describe the active member who created the invite, rather than only the Captain.

## App Link Hosting Note

`https://paysplit.app/join/{code}` opens the installed mobile app only after the domain association files are hosted over HTTPS. The web or infrastructure owner must publish the Apple Universal Links association file at `/.well-known/apple-app-site-association` and the Android App Links association file at `/.well-known/assetlinks.json`. Their application identifiers and signing certificate values come from the registered mobile applications and must not be guessed in this API document.

Both association files must authorize the `/join/*` path. The backend preview endpoint remains the sole server authority for the code after the mobile router receives the path. If the app is not installed, the URL serves a web fallback page. Deferred deep link support after installation is not part of this API change and needs a separate provider decision.

## Required API Changes

### List Active Invites

Add `GET /api/v1/groups/{id}/invites`.

An active group member may call this endpoint. The response returns only unrevoked, unexpired, and unexhausted invites for the group, ordered by newest first. Each item contains `id`, `code`, `invite_url`, `expires_at`, nullable `max_uses`, and `use_count`.

Return `404 GROUP_NOT_FOUND` when the group does not exist or the caller is not an active member. Do not expose inactive or historical codes.

### Create Or Reuse Invite

Change `POST /api/v1/groups/{id}/invites` so every active group member may create or reuse the current active invite with an empty request body.

An active Captain may additionally provide `expires_in_hours`, `max_uses`, and `regenerate`. Only a Captain may set `regenerate: true`. A non Captain request carrying any configuration field returns `403 CAPTAIN_REQUIRED`.

The empty member request reuses the newest available invite when one exists. Otherwise it creates a default invite with the existing Backend defaults. This keeps normal member sharing idempotent and avoids duplicate active codes.

### Revoke Invite

Keep `DELETE /api/v1/groups/{id}/invites/{inviteId}` restricted to the active Captain.

## Security And Audit Requirements

Invite codes are sensitive. Return a raw code only from the list and create or reuse responses to active members of that group. Redact codes and invite URLs from HTTP access logs, application logs, activity metadata, analytics, and error reports. Rate limit preview and join attempts by account and IP address, and return the same public error shape for an unknown, expired, revoked, or exhausted code.

Write an `invite_created` activity when a new invite is created. Reusing an existing invite does not write an activity. Include the invite ID and policy fields in activity metadata, never the code or URL.

## Frontend Dependency

`PaySplit-FE/docs/specs/0003-group-hub-bills-ui-v1.md` requires this API for the Invite Code Bar and member invite management view.
