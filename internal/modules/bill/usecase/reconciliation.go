package usecase

import (
	"errors"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/domain"
	platformmetrics "paysplit-backend/internal/platform/metrics"
)

// Mã chặn ổn định trả về cho client. Chúng gia nhập SUBTOTAL_MISMATCH và TOTAL_MISMATCH đã có,
// để đường đọc, review và chốt sổ cùng nói một điều (Spec 3 AC-5, AC-6, AC-7).
const (
	// BlockerItemUnassigned báo có món chưa được gán cho thành viên nào.
	BlockerItemUnassigned = "ITEM_UNASSIGNED"

	// BlockerInactiveMemberAssigned báo có món được gán cho thành viên đã rời nhóm.
	BlockerInactiveMemberAssigned = "INACTIVE_MEMBER_ASSIGNED"

	// BlockerDiscountExceedsBill báo giảm giá lớn hơn tiền hàng cộng phí cộng VAT.
	BlockerDiscountExceedsBill = "DISCOUNT_EXCEEDS_BILL"

	// BlockerDiscountNotAllocatable báo giảm giá hợp lệ so với tổng nhưng dồn vào những thành viên
	// không hấp thụ hết, khiến phần bị chặn trần đẩy Creditor xuống âm (Spec 3 AC-10).
	BlockerDiscountNotAllocatable = "DISCOUNT_NOT_ALLOCATABLE"

	// BlockerCreditorRequired báo hóa đơn chưa có Creditor nên không thể phân bổ tiền.
	BlockerCreditorRequired = "CREDITOR_REQUIRED"
)

// evaluateAllocation chạy toàn bộ phần đối soát của một hóa đơn và, nếu sạch, chạy luôn phân bổ.
//
// Đây là một nguồn sự thật duy nhất cho ba đường đi vốn trước đây lặp lại nhau và dễ lệch nhau:
// đọc chi tiết hóa đơn, review, và chốt sổ. Đường đọc dùng danh sách mã chặn để báo cho client biết
// vì sao chưa có breakdown, thay vì trả về một response trống không giải thích gì (Spec 3 AC-6).
//
// activeMemberIDs có thể là nil khi người gọi không tra được danh sách thành viên hoạt động. Khi đó
// bước kiểm tra thành viên đã rời nhóm bị bỏ qua, các bước còn lại vẫn chạy.
//
// Trả về danh sách mã chặn theo thứ tự tất định. Danh sách rỗng nghĩa là hóa đơn sẵn sàng, và khi đó
// allocations luôn khác nil.
func evaluateAllocation(bill *domain.Bill, activeMemberIDs map[uuid.UUID]bool) ([]*MemberAllocation, []string) {
	blockers := make([]string, 0, 4)

	var sumItems int64
	hasUnassigned := false
	hasInactive := false
	for _, it := range bill.Items {
		sumItems += it.LineTotal
		if len(it.Assignments) == 0 {
			hasUnassigned = true
			continue
		}
		if activeMemberIDs != nil {
			for _, a := range it.Assignments {
				if !activeMemberIDs[a.MemberID] {
					hasInactive = true
				}
			}
		}
	}
	if hasUnassigned {
		blockers = append(blockers, BlockerItemUnassigned)
	}
	if hasInactive {
		blockers = append(blockers, BlockerInactiveMemberAssigned)
	}
	if bill.CreditorMemberID == uuid.Nil || (activeMemberIDs != nil && !activeMemberIDs[bill.CreditorMemberID]) {
		blockers = append(blockers, BlockerCreditorRequired)
	}

	if bill.Discount > bill.Subtotal+bill.ServiceCharge+bill.VAT {
		blockers = append(blockers, BlockerDiscountExceedsBill)
	}
	if bill.Subtotal != sumItems {
		blockers = append(blockers, domain.WarningSubtotalMismatch)
	}
	if bill.Total != bill.Subtotal+bill.ServiceCharge+bill.VAT-bill.Discount {
		blockers = append(blockers, domain.WarningTotalMismatch)
	}

	// Chỉ chạy phân bổ khi phần đối soát rẻ đã sạch. Chạy sớm hơn thì lỗi trả về chỉ phản ánh dữ
	// liệu đã sai từ trước, không giúp gì cho người dùng.
	if len(blockers) > 0 {
		return nil, blockers
	}

	allocations, err := CalculateAllocation(toAllocationInput(bill))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrDiscountNotAllocatable):
			return nil, []string{BlockerDiscountNotAllocatable}
		case errors.Is(err, domain.ErrCreditorRequired):
			return nil, []string{BlockerCreditorRequired}
		default:
			// Bất biến trong phân bổ bị phá vỡ. Không có mã ổn định nào mô tả được, và im lặng ở đây
			// chính là lỗi cũ, nên chặn hóa đơn lại.
			return nil, []string{domain.WarningTotalMismatch}
		}
	}

	return allocations, nil
}

// blockersToError ánh xạ mã chặn đầu tiên sang lỗi domain tương ứng cho review và chốt sổ.
func blockersToError(blockers []string) error {
	for _, b := range blockers {
		switch b {
		case BlockerDiscountNotAllocatable:
			return domain.ErrDiscountNotAllocatable
		case BlockerCreditorRequired:
			return domain.ErrCreditorRequired
		}
	}
	return domain.ErrBillNotReady
}

// recordBlockerMetric ghi nhận mã chặn đầu tiên vào paysplit_bill_mismatch_block_total. Nhãn giữ
// nguyên như trước khi gom chung, để dashboard hiện có không bị vỡ (Spec 3 AC-14).
func recordBlockerMetric(blockers []string) {
	if len(blockers) == 0 {
		return
	}
	label := "totals_mismatch"
	switch blockers[0] {
	case BlockerItemUnassigned:
		label = "item_unassigned"
	case BlockerInactiveMemberAssigned:
		label = "inactive_member_assigned"
	case BlockerDiscountNotAllocatable:
		label = "discount_not_allocatable"
	case BlockerCreditorRequired:
		label = "creditor_required"
	}
	platformmetrics.RecordBillMismatchBlock(label)
}

// mergeMismatchCodes gộp mã chặn tính tại thời điểm đọc với cảnh báo OCR đã lưu, giữ nguyên thứ tự
// và bỏ trùng. Cảnh báo OCR như OCR_DATE_AMBIGUOUS không được mất khi ta tính lại đối soát.
//
// Luôn trả về slice khác nil. Trường mismatch_codes không có omitempty, nên trả nil sẽ biến nó
// thành null trong JSON, và client gọi isEmpty trên null sẽ vỡ. Hóa đơn sạch phải là mảng rỗng.
func mergeMismatchCodes(stored, computed []string) []string {
	seen := make(map[string]bool, len(stored)+len(computed))
	out := make([]string, 0, len(stored)+len(computed))
	for _, group := range [][]string{stored, computed} {
		for _, c := range group {
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
