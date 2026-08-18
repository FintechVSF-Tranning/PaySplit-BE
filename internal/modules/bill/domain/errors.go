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
)
