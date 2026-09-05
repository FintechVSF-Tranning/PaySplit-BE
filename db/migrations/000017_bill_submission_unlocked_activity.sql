-- +goose Up
-- Mở khóa ghi hoạt động trong cùng transaction với cập nhật nhóm.
-- Thiếu giá trị enum này khiến INSERT lỗi và rollback cả thao tác mở khóa.
-- +goose StatementBegin
DO $$
BEGIN
    ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'bill_submission_unlocked';
END $$;
-- +goose StatementEnd

-- +goose Down
-- PostgreSQL không hỗ trợ xóa riêng một giá trị enum. Giữ lại để bảo toàn
-- lịch sử mở khóa đã ghi, tương tự các giá trị thêm ở migration 000012.
SELECT 1;
