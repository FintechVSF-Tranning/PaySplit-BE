# 0009. Group realtime sync v1

**Date**: 2026-08-26
**Status**: Implemented
**Target release**: V1

## Summary

Danh sách thành viên của một nhóm được đồng bộ tới mọi thiết bị với độ trễ
50–200 ms, thay vì chờ người dùng kéo refresh.

Giao thức gồm ba lớp: `GET /groups/{id}` dựng trạng thái xuất phát kèm version,
`GET /groups/{id}/events` (SSE) đẩy delta, và `GET /groups/{id}/sync?since=` hàn
mọi lỗ hổng. Kênh đẩy được phép mất gói — lớp thứ ba khiến điều đó chỉ làm tăng
độ trễ chứ không làm sai dữ liệu.

Phân tích lựa chọn đầy đủ, gồm cả các phương án đơn giản hơn và khó hơn đã bị
loại: [rationale.md](rationale.md).

## Acceptance criteria

**AC-1** — Mỗi nhóm có `roster_version` đơn điệu tăng. Dãy version liền mạch và
đúng thứ tự commit, kể cả khi nhiều mutation chạy song song.

**AC-2** — Mọi mutation roster ghi đúng một dòng `group_events` trong **cùng
transaction** với thay đổi dữ liệu. Transaction bị rollback không để lại sự kiện
và không tiêu tốn version.

**AC-3** — `pg_notify` được gọi bên trong transaction, nên client không bao giờ
nhận sự kiện của một transaction bị rollback.

**AC-4** — `GET /groups/{id}` trả `version` và `caller_membership_id`, đọc trong
một transaction `REPEATABLE READ` để version khớp đúng danh sách thành viên đi kèm.

**AC-5** — `GET /groups/{id}/sync?since=N` trả `mode=delta` khi client còn đủ
gần, `mode=snapshot` khi `since<=0`, khi `since` lớn hơn version hiện tại, khi
nhật ký đã bị dọn qua mốc `since`, hoặc khi delta bị cắt vì chạm giới hạn.

**AC-6** — `GET /groups/{id}/events` xác thực caller là thành viên active, đăng
ký nhận sự kiện **trước** khi đọc trạng thái ban đầu, và không phát trùng sự kiện
đã nằm trong phần khởi tạo.

**AC-7** — Stream phát `close` kèm lý do khi chạm tuổi thọ tối đa
(`max_connection_age`), khi nhóm bị giải tán (`group_archived`), hoặc khi chính
caller không còn là thành viên (`membership_ended`).

**AC-8** — Thân frame SSE của sự kiện nhật ký mang đúng shape với phần tử
`events` của `/sync`, để client dùng chung một hàm giải mã.

**AC-9** — Payload rời server mang `avatar_url`, không bao giờ mang
`avatar_object_key`.

**AC-10** — Client giữ `version` cục bộ; version nhảy cóc thì gọi `/sync` thay vì
đoán phần thiếu; version nhỏ hơn hoặc bằng thì bỏ qua.

**AC-11** — Client kết nối lại với backoff **có jitter**, và đồng bộ lại khi app
resume.

**AC-12** — Nhật ký cũ hơn `GROUP_EVENT_RETENTION_DAYS` được dọn định kỳ. Client
tụt xa hơn ngưỡng đó nhận snapshot.

## Surface

| Method & Path | Ghi chú |
|---|---|
| `GET /api/v1/groups/{id}/sync?since=` | Catch-up nguội. Bọc `SuccessEnvelope`. |
| `GET /api/v1/groups/{id}/events?since=` | SSE, `text/event-stream`, **không** bọc envelope. |

Trường mới trên `GET /groups/{id}`: `version` (bắt buộc), `caller_membership_id`
(bắt buộc).

## Configuration

| Biến | Mặc định |
|---|---|
| `GROUP_SSE_HEARTBEAT_INTERVAL_SECONDS` | 15 |
| `GROUP_SSE_MAX_CONNECTION_AGE_MINUTES` | 15 |
| `GROUP_EVENT_RETENTION_DAYS` | 7 |
