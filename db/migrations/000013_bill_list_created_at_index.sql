-- +goose NO TRANSACTION
-- +goose Up
-- ---------------------------------------------------------------------------
-- ListBillsByGroup / ListBillsByGroupCursor sắp xếp theo (created_at DESC, id DESC)
-- nhưng chỉ có idx_bills_group_status(group_id, status) để bám vào. Postgres phải
-- join group_members + users và chạy LATERAL đếm tiến độ cho TOÀN BỘ bill của nhóm
-- rồi mới sort và cắt LIMIT.
--
-- Index này khớp đúng thứ tự sort nên planner lấy được top-N trực tiếp từ index và
-- chỉ chạy LATERAL cho đúng số dòng trả về.
-- CONCURRENTLY vì bills nhận ghi liên tục; NO TRANSACTION vì CONCURRENTLY không
-- chạy được trong transaction block.
-- ---------------------------------------------------------------------------

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_bills_group_created_at
    ON bills(group_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_bills_group_created_at;
