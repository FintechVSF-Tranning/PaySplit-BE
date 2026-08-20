package usecase

import (
	"testing"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/domain"
)

func billFixture(creditor uuid.UUID, items []*domain.BillItem, subtotal, sc, vat, disc, total int64) *domain.Bill {
	return &domain.Bill{
		ID:               uuid.New(),
		CreditorMemberID: creditor,
		Subtotal:         subtotal,
		ServiceCharge:    sc,
		VAT:              vat,
		Discount:         disc,
		Total:            total,
		Items:            items,
	}
}

func itemFixture(lineTotal int64, memberIDs ...uuid.UUID) *domain.BillItem {
	it := &domain.BillItem{ID: uuid.New(), LineTotal: lineTotal}
	for _, m := range memberIDs {
		it.Assignments = append(it.Assignments, &domain.BillItemAssignment{
			ID: uuid.New(), MemberID: m, Weight: "1.0000",
		})
	}
	return it
}

func hasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

func TestEvaluateAllocation_CleanBill_NoBlockers(t *testing.T) {
	// covers: AC-5, AC-6 (hóa đơn sạch thì không có mã chặn và có breakdown)
	c, m := uuid.New(), uuid.New()
	bill := billFixture(c, []*domain.BillItem{itemFixture(100001, c, m)}, 100001, 0, 0, 0, 100001)

	allocs, blockers := evaluateAllocation(bill, map[uuid.UUID]bool{c: true, m: true})
	if len(blockers) != 0 {
		t.Fatalf("mong đợi không có mã chặn, nhận %v", blockers)
	}
	if len(allocs) != 2 {
		t.Fatalf("mong đợi 2 phần chia, nhận %d", len(allocs))
	}
	var sum int64
	for _, a := range allocs {
		sum += a.FinalAmount
	}
	if sum != 100001 {
		t.Errorf("tổng phải bằng 100001, nhận %d", sum)
	}
}

func TestEvaluateAllocation_ItemUnassigned(t *testing.T) {
	// covers: AC-6 (món chưa gán ai thì chặn)
	c := uuid.New()
	bill := billFixture(c, []*domain.BillItem{itemFixture(1000)}, 1000, 0, 0, 0, 1000)

	allocs, blockers := evaluateAllocation(bill, map[uuid.UUID]bool{c: true})
	if !hasCode(blockers, BlockerItemUnassigned) {
		t.Fatalf("mong đợi %s, nhận %v", BlockerItemUnassigned, blockers)
	}
	if allocs != nil {
		t.Error("không được trả breakdown khi còn mã chặn")
	}
}

func TestEvaluateAllocation_InactiveMemberAssigned(t *testing.T) {
	// covers: AC-7 (gán cho người đã rời nhóm thì chặn)
	c, gone := uuid.New(), uuid.New()
	bill := billFixture(c, []*domain.BillItem{itemFixture(1000, gone)}, 1000, 0, 0, 0, 1000)

	if _, blockers := evaluateAllocation(bill, map[uuid.UUID]bool{c: true}); !hasCode(blockers, BlockerInactiveMemberAssigned) {
		t.Fatalf("mong đợi %s, nhận %v", BlockerInactiveMemberAssigned, blockers)
	}
}

func TestEvaluateAllocation_NilActiveSet_SkipsMembershipCheck(t *testing.T) {
	// covers: AC-6 (không tra được danh sách thành viên thì bỏ qua đúng một bước, phần còn lại vẫn chạy)
	c, gone := uuid.New(), uuid.New()
	bill := billFixture(c, []*domain.BillItem{itemFixture(1000, gone)}, 1000, 0, 0, 0, 1000)

	if _, blockers := evaluateAllocation(bill, nil); len(blockers) != 0 {
		t.Fatalf("mong đợi không có mã chặn khi bỏ qua kiểm tra thành viên, nhận %v", blockers)
	}
}

func TestEvaluateAllocation_SubtotalAndTotalMismatch(t *testing.T) {
	// covers: AC-5 (subtotal và total khai báo lệch với thực tế thì cả hai mã cùng xuất hiện)
	c, m := uuid.New(), uuid.New()
	bill := billFixture(c, []*domain.BillItem{itemFixture(1000, c, m)}, 5000, 0, 0, 0, 9999)

	_, blockers := evaluateAllocation(bill, map[uuid.UUID]bool{c: true, m: true})
	if !hasCode(blockers, domain.WarningSubtotalMismatch) {
		t.Errorf("mong đợi %s, nhận %v", domain.WarningSubtotalMismatch, blockers)
	}
	if !hasCode(blockers, domain.WarningTotalMismatch) {
		t.Errorf("mong đợi %s, nhận %v", domain.WarningTotalMismatch, blockers)
	}
}

func TestEvaluateAllocation_DiscountExceedsBill(t *testing.T) {
	// covers: AC-10 (giảm giá lớn hơn cả hóa đơn thì chặn bằng mã riêng)
	c, m := uuid.New(), uuid.New()
	bill := billFixture(c, []*domain.BillItem{itemFixture(1000, c, m)}, 1000, 0, 0, 5000, -4000)

	if _, blockers := evaluateAllocation(bill, map[uuid.UUID]bool{c: true, m: true}); !hasCode(blockers, BlockerDiscountExceedsBill) {
		t.Fatalf("mong đợi %s, nhận %v", BlockerDiscountExceedsBill, blockers)
	}
}

func TestEvaluateAllocation_MissingCreditor(t *testing.T) {
	// covers: AC-6 (không có Creditor thì không có người hấp thụ dư)
	m := uuid.New()
	bill := billFixture(uuid.Nil, []*domain.BillItem{itemFixture(1000, m)}, 1000, 0, 0, 0, 1000)

	if _, blockers := evaluateAllocation(bill, map[uuid.UUID]bool{m: true}); !hasCode(blockers, BlockerCreditorRequired) {
		t.Fatalf("mong đợi %s, nhận %v", BlockerCreditorRequired, blockers)
	}
}

func TestBlockersToError(t *testing.T) {
	cases := []struct {
		name     string
		blockers []string
		want     error
	}{
		{"mặc định", []string{domain.WarningTotalMismatch}, domain.ErrBillNotReady},
		{"giảm giá không chia được", []string{BlockerDiscountNotAllocatable}, domain.ErrDiscountNotAllocatable},
		{"thiếu creditor", []string{BlockerCreditorRequired}, domain.ErrCreditorRequired},
		{"ưu tiên mã cụ thể", []string{BlockerItemUnassigned, BlockerCreditorRequired}, domain.ErrCreditorRequired},
		{"rỗng", nil, domain.ErrBillNotReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blockersToError(tc.blockers); got != tc.want {
				t.Errorf("mong đợi %v, nhận %v", tc.want, got)
			}
		})
	}
}

func TestMergeMismatchCodes(t *testing.T) {
	// Cảnh báo OCR đã lưu không được mất khi tính lại đối soát, và không được lặp.
	got := mergeMismatchCodes(
		[]string{domain.WarningOCRDateAmbiguous, domain.WarningSubtotalMismatch},
		[]string{domain.WarningSubtotalMismatch, BlockerItemUnassigned},
	)
	want := []string{domain.WarningOCRDateAmbiguous, domain.WarningSubtotalMismatch, BlockerItemUnassigned}
	if len(got) != len(want) {
		t.Fatalf("mong đợi %v, nhận %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mong đợi %v, nhận %v", want, got)
		}
	}
	// Hóa đơn sạch phải cho ra mảng rỗng chứ không phải nil, nếu không JSON trả về null và client
	// gọi isEmpty trên null sẽ vỡ.
	empty := mergeMismatchCodes(nil, nil)
	if empty == nil {
		t.Error("không có mã nào thì phải trả về mảng rỗng, không phải nil")
	}
	if len(empty) != 0 {
		t.Errorf("mong đợi mảng rỗng, nhận %v", empty)
	}
}
