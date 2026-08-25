package domain

import "errors"

var (
	// ErrInvalidInput đánh dấu request không hợp lệ về mặt dữ liệu đầu vào.
	ErrInvalidInput = errors.New("invalid input")

	// ErrForbidden trả về khi caller không có quyền thực hiện hành động này.
	ErrForbidden = errors.New("forbidden")

	// ErrBillNotFound trả về khi hóa đơn không tồn tại hoặc caller không có quyền truy cập trong group.
	ErrBillNotFound = errors.New("bill not found")

	// ErrBillConflict trả về khi version cung cấp không khớp hoặc hóa đơn đã bị thay đổi đồng thời.
	ErrBillConflict = errors.New("bill conflict")

	// ErrVersionConflict trả về khi version cung cấp không khớp với version hiện tại trong database.
	ErrVersionConflict = errors.New("version conflict")

	// ErrOcrJobNotFound trả về khi không tìm thấy OCR job tương ứng.
	ErrOcrJobNotFound = errors.New("ocr job not found")

	// ErrOcrJobConflict trả về khi version của OCR job không khớp.
	ErrOcrJobConflict = errors.New("ocr job conflict")

	// ErrBillImmutable trả về khi cố gắng thay đổi một hóa đơn đã finalized.
	ErrBillImmutable = errors.New("bill is immutable")

	// ErrBillNotReady trả về khi hóa đơn chưa thỏa mãn các ràng buộc về đối soát hoặc tỷ lệ để review/finalize.
	ErrBillNotReady = errors.New("bill not ready")

	// ErrReviewRequired trả về khi finalize yêu cầu hóa đơn phải được review trước ở đúng version hiện tại.
	ErrReviewRequired = errors.New("review required")

	// ErrImagesRequired trả về khi yêu cầu chạy OCR nhưng hóa đơn không có ảnh receipt nào.
	ErrImagesRequired = errors.New("images required for ocr")

	// ErrOcrProviderUnavailable trả về khi nhà cung cấp OCR không khả dụng hoặc thiếu cấu hình API Key.
	ErrOcrProviderUnavailable = errors.New("ocr provider unavailable")

	// ErrOcrSchemaInvalid trả về khi phản hồi từ AI không đúng cấu trúc schema hợp lệ.
	ErrOcrSchemaInvalid = errors.New("ocr schema invalid")

	// ErrOcrTimeout trả về khi quá trình bóc tách OCR vượt quá thời gian timeout quy định.
	ErrOcrTimeout = errors.New("ocr timeout")

	// ErrOcrResultStale trả về khi áp dụng kết quả OCR nhưng version của bill đã thay đổi sau khi job bắt đầu.
	ErrOcrResultStale = errors.New("ocr result stale")

	// ErrOcrAlreadyRunning trả về khi bill đã có 1 job OCR đang ở trạng thái queued hoặc processing.
	ErrOcrAlreadyRunning = errors.New("ocr job already running")

	// ErrOcrLimitReached trả về khi người dùng vượt quá số lần retry OCR thủ công cho phép trong 24h.
	ErrOcrLimitReached = errors.New("ocr limit reached")

	// ErrOcrNotReady trả về khi cố gắng apply một job OCR chưa hoàn tất thành công.
	ErrOcrNotReady = errors.New("ocr not ready")

	// ErrOcrAlreadyApplied trả về khi candidate đã được apply trước đó.
	ErrOcrAlreadyApplied = errors.New("ocr already applied")

	// ErrOcrCandidateInvalid trả về khi candidate có dữ liệu không hợp lệ không thể áp dụng vào bill.
	ErrOcrCandidateInvalid = errors.New("ocr candidate invalid")

	// ErrPaymentAlreadyStarted trả về khi cố gắng hủy hóa đơn có khoản nợ đã bắt đầu thanh toán.
	ErrPaymentAlreadyStarted = errors.New("payment already started")

	// ErrBillNotFinalized trả về khi cố gắng hủy hóa đơn chưa được finalize (Spec 3 AC-11).
	ErrBillNotFinalized = errors.New("bill not finalized")

	// ErrBillAlreadyVoided trả về khi cố gắng hủy hóa đơn đã bị hủy trước đó (Spec 3 AC-11).
	ErrBillAlreadyVoided = errors.New("bill already voided")

	// ErrBankAccountRequired trả về khi finalize hóa đơn nhưng Creditor chưa cấu hình tài khoản ngân hàng (Spec 3 AC-9).
	ErrBankAccountRequired = errors.New("bank account required")

	// ErrInvalidCursor trả về khi cursor phân trang không hợp lệ (Spec 3 AC-12).
	ErrInvalidCursor = errors.New("invalid cursor")

	// ErrCreditorRequired trả về khi hóa đơn chưa có Creditor nên không thể phân bổ tiền. Không có
	// người hấp thụ phần dư thứ hai, nên đây là điều kiện bắt buộc (Spec 3 AC-6, AC-10).
	ErrCreditorRequired = errors.New("creditor required for allocation")

	// ErrDiscountNotAllocatable trả về khi giảm giá hợp lệ so với tổng hóa đơn nhưng dồn vào những
	// thành viên không hấp thụ hết, khiến phần bị chặn trần đẩy Creditor xuống âm (Spec 3 AC-10).
	ErrDiscountNotAllocatable = errors.New("discount not allocatable across members")

	// ErrIdempotencyInProgress trả về khi request cùng idempotency key đang được xử lý (Spec 3 AC-1, AC-9).
	ErrIdempotencyInProgress = errors.New("idempotency in progress")

	// ErrIdempotencyKeyReused trả về khi idempotency key bị tái sử dụng với request payload khác (Spec 3 AC-1, AC-9).
	ErrIdempotencyKeyReused = errors.New("idempotency key reused with different request payload")

	// ErrSubmissionLocked trả về khi nhóm đã bị khóa gửi hóa đơn mới và một request
	// tạo bill (thủ công hoặc ảnh) vẫn cố tạo (Spec 0008 AC-2).
	ErrSubmissionLocked = errors.New("bill submission locked")

	// ErrCaptainRequired trả về khi hành động khóa gửi hóa đơn hoặc chốt toàn bộ
	// chỉ dành cho Captain đang hoạt động nhưng caller không phải Captain (Spec 0008 AC-10).
	ErrCaptainRequired = errors.New("active captain required")

	// ErrBatchNotFound trả về khi batch chốt toàn bộ không tồn tại trong nhóm (Spec 0008 AC-6).
	ErrBatchNotFound = errors.New("finalize batch not found")

	// ErrGroupNotFound trả về khi nhóm không tồn tại hoặc caller không phải thành
	// viên active của nhóm đang hoạt động, cho các endpoint mới của spec 0008
	// (lock và batch) theo đúng API surface: 404 GROUP_NOT_FOUND.
	ErrGroupNotFound = errors.New("group not found")
)

// BulkFinalizeInProgressError trả về kèm ID của batch đang queued/processing để
// Captain có thể tiếp tục với batch đó thay vì mở batch thứ hai (Spec 0008 AC-4, AC-7).
type BulkFinalizeInProgressError struct {
	ActiveBatchID string
}

func (e *BulkFinalizeInProgressError) Error() string { return "bulk finalize already in progress" }
