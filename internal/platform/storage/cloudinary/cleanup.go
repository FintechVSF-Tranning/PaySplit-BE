package cloudinary

import (
	"context"
	"strings"
)

// billObjectKeyPrefix khớp với quy ước public ID của BillStorage.Upload: "bills/{operation_id}/{position}".
const billObjectKeyPrefix = "bills/"

// CleanupDeleter là interface tối thiểu mà cả AvatarStorage và BillStorage đều thỏa mãn.
type CleanupDeleter interface {
	Delete(ctx context.Context, publicID string) error
}

// CleanupStorage định tuyến một object key dọn dẹp tới đúng adapter Cloudinary theo loại tài sản
// (avatar dùng delivery type "upload", ảnh hóa đơn dùng "private"). Dùng chung một deleter cho cả
// hai loại sẽ luôn "not found" cho loại còn lại vì destroy type không khớp (Spec 3 AC-13).
type CleanupStorage struct {
	avatar CleanupDeleter
	bill   CleanupDeleter
}

// NewCleanupStorage khởi tạo bộ định tuyến xóa dùng cho media_cleanup_jobs worker dùng chung.
func NewCleanupStorage(avatar, bill CleanupDeleter) *CleanupStorage {
	return &CleanupStorage{avatar: avatar, bill: bill}
}

// Delete chọn đúng adapter dựa trên tiền tố của object key rồi ủy quyền việc xóa.
func (s *CleanupStorage) Delete(ctx context.Context, objectKey string) error {
	if strings.HasPrefix(objectKey, billObjectKeyPrefix) {
		return s.bill.Delete(ctx, objectKey)
	}
	return s.avatar.Delete(ctx, objectKey)
}
