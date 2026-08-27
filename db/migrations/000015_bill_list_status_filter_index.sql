-- +goose NO TRANSACTION
-- +goose Up
-- ---------------------------------------------------------------------------
-- Tab "Hóa đơn trong nhóm" lọc theo status (Nháp / Chờ duyệt / Đã chốt / Đã hủy)
-- rồi phân trang keyset theo (created_at DESC, id DESC).
--
-- Hai index đang có đều thiếu một nửa: idx_bills_group_status(group_id, status)
-- lọc được nhưng không giữ thứ tự sort, còn idx_bills_group_created_at
-- (group_id, created_at DESC, id DESC) giữ thứ tự nhưng phải quét và loại bỏ
-- mọi bill khác trạng thái. Nhóm nhiều bản nháp mà lọc "Đã hủy" là trường hợp
-- xấu nhất: quét gần hết nhóm mới gom đủ một trang.
--
-- Index này gộp cả hai nên planner lấy thẳng top-N của đúng trạng thái. Truy vấn
-- "Tất cả" (mảng lọc rỗng) vẫn dùng idx_bills_group_created_at như trước.
-- CONCURRENTLY vì bills nhận ghi liên tục; NO TRANSACTION vì CONCURRENTLY không
-- chạy được trong transaction block.
-- ---------------------------------------------------------------------------

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_bills_group_status_created_at
    ON bills(group_id, status, created_at DESC, id DESC);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_bills_group_status_created_at;
