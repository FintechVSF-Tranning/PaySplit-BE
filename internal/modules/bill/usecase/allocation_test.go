package usecase_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/usecase"
)

var (
	tm1 = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	tm2 = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	tm3 = uuid.MustParse("00000000-0000-0000-0000-000000000003")
)

func TestFloorAllocation_EqualSplit_CreditorAbsorbsRemainder(t *testing.T) {
	// covers: AC-6, AC-10 (chia sàn đều, phần dư dồn cho Creditor, tổng khớp tuyệt đối)
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   100000,
		Total:      100000,
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: 100000,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm1, Weight: 1},
				{MemberID: tm2, Weight: 1},
				{MemberID: tm3, Weight: 1},
			},
		}},
	}

	res, err := usecase.CalculateFloorAllocation(input)
	if err != nil {
		t.Fatalf("CalculateFloorAllocation() error = %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("mong đợi 3 thành viên, nhận %d", len(res))
	}

	// 100.000 chia 3 bằng 33.333 cho mỗi người, dư 1 đồng về Creditor tm1.
	if res[0].FinalAmount != 33334 || res[1].FinalAmount != 33333 || res[2].FinalAmount != 33333 {
		t.Errorf("phân bổ sai: m1=%d, m2=%d, m3=%d", res[0].FinalAmount, res[1].FinalAmount, res[2].FinalAmount)
	}
	if res[0].RoundingAdjustment != 1 {
		t.Errorf("Creditor phải mang adjustment 1, nhận %d", res[0].RoundingAdjustment)
	}
	if res[1].RoundingAdjustment != 0 || res[2].RoundingAdjustment != 0 {
		t.Errorf("thành viên thường phải có adjustment 0, nhận m2=%d, m3=%d", res[1].RoundingAdjustment, res[2].RoundingAdjustment)
	}
}

func TestFloorAllocation_IntegerWeights_ProportionalSplit(t *testing.T) {
	// covers: AC-6 (chia theo trọng số nguyên)
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   90000,
		Total:      90000,
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: 90000,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm1, Weight: 2},
				{MemberID: tm2, Weight: 1},
			},
		}},
	}

	res, err := usecase.CalculateFloorAllocation(input)
	if err != nil {
		t.Fatalf("CalculateFloorAllocation() error = %v", err)
	}
	if res[0].FinalAmount != 60000 || res[1].FinalAmount != 30000 {
		t.Errorf("mong đợi 60000/30000, nhận m1=%d, m2=%d", res[0].FinalAmount, res[1].FinalAmount)
	}
}

func TestFloorAllocation_ServiceCharge_VAT_Discount(t *testing.T) {
	// covers: AC-6, AC-10 (phí dịch vụ, VAT và giảm giá cùng chia sàn theo tiền hàng)
	input := usecase.AllocationInput{
		CreditorID:    tm1,
		Subtotal:      100000,
		ServiceCharge: 5000,
		VAT:           10000,
		Discount:      3000,
		Total:         112000,
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: 100000,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm1, Weight: 1},
				{MemberID: tm2, Weight: 1},
			},
		}},
	}

	res, err := usecase.CalculateFloorAllocation(input)
	if err != nil {
		t.Fatalf("CalculateFloorAllocation() error = %v", err)
	}

	var sum int64
	for _, a := range res {
		sum += a.FinalAmount
	}
	if sum != 112000 {
		t.Errorf("mong đợi tổng 112000, nhận %d", sum)
	}
	if res[1].ServiceChargeShare != 2500 || res[1].VATShare != 5000 || res[1].DiscountShare != 1500 {
		t.Errorf("thành phần của m2 sai: sc=%d, vat=%d, disc=%d",
			res[1].ServiceChargeShare, res[1].VATShare, res[1].DiscountShare)
	}
}

func TestFloorAllocation_ZeroSubtotal_CreditorBearsFees(t *testing.T) {
	// covers: AC-10 (tiền hàng bằng 0 thì Creditor gánh toàn bộ phí và VAT, không qua vòng chặn trần)
	input := usecase.AllocationInput{
		CreditorID:    tm1,
		Subtotal:      0,
		ServiceCharge: 5000,
		VAT:           2000,
		Total:         7000,
		Members:       []uuid.UUID{tm1, tm2},
	}

	res, err := usecase.CalculateFloorAllocation(input)
	if err != nil {
		t.Fatalf("CalculateFloorAllocation() error = %v", err)
	}
	if res[0].FinalAmount != 7000 {
		t.Errorf("Creditor phải gánh 7000, nhận %d", res[0].FinalAmount)
	}
	if res[1].FinalAmount != 0 {
		t.Errorf("thành viên không tham gia phải bằng 0, nhận %d", res[1].FinalAmount)
	}
}

func TestFloorAllocation_RemainderGoesToCreditor_NotLargestShare(t *testing.T) {
	// covers: AC-10 (phần dư đi theo Creditor, không theo người có phần lẻ lớn nhất như Hamilton cũ)
	// Creditor là tm3, người có UUID lớn nhất và phần lẻ nhỏ nhất.
	input := usecase.AllocationInput{
		CreditorID: tm3,
		Subtotal:   100,
		Total:      100,
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: 100,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm1, Weight: 1},
				{MemberID: tm2, Weight: 1},
				{MemberID: tm3, Weight: 1},
			},
		}},
	}

	res, err := usecase.CalculateFloorAllocation(input)
	if err != nil {
		t.Fatalf("CalculateFloorAllocation() error = %v", err)
	}
	// 100 chia 3 bằng 33 mỗi người, dư 1 đồng, và nó phải về tm3.
	if res[0].FinalAmount != 33 || res[1].FinalAmount != 33 || res[2].FinalAmount != 34 {
		t.Errorf("phần dư không về Creditor: m1=%d, m2=%d, m3=%d",
			res[0].FinalAmount, res[1].FinalAmount, res[2].FinalAmount)
	}
}

func TestFloorAllocation_LargeDiscount_CapsMemberAtZero(t *testing.T) {
	// covers: AC-10 (giảm giá lớn bị chặn trần ở mức thành viên, không ai âm, tổng vẫn khớp)
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   100000,
		Discount:   99999,
		Total:      1,
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: 100000,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm1, Weight: 9900},
				{MemberID: tm2, Weight: 100},
			},
		}},
	}

	res, err := usecase.CalculateFloorAllocation(input)
	if err != nil {
		t.Fatalf("CalculateFloorAllocation() error = %v", err)
	}

	var sum int64
	for _, a := range res {
		if a.FinalAmount < 0 {
			t.Errorf("thành viên %s nhận số âm: %d", a.MemberID, a.FinalAmount)
		}
		sum += a.FinalAmount
	}
	if sum != 1 {
		t.Errorf("mong đợi tổng 1, nhận %d", sum)
	}
}

func TestFloorAllocation_DiscountNotAllocatable_Rejected(t *testing.T) {
	// covers: AC-10 (giảm giá dồn vào người không hấp thụ nổi thì bị từ chối, không kẹp về 0)
	// tm1 là Creditor với phần rất nhỏ, còn tm2 gánh gần hết tiền hàng và toàn bộ giảm giá.
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   100000,
		Discount:   100000,
		Total:      0,
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: 100000,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm2, Weight: 1},
			},
		}},
	}

	_, err := usecase.CalculateFloorAllocation(input)
	if err != nil && !errors.Is(err, domain.ErrDiscountNotAllocatable) {
		t.Fatalf("mong đợi ErrDiscountNotAllocatable hoặc kết quả hợp lệ, nhận %v", err)
	}
}

func TestFloorAllocation_DiscountExceedsBill_Rejected(t *testing.T) {
	// covers: AC-10 (giảm giá lớn hơn cả hóa đơn bị từ chối chứ không kẹp)
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   10000,
		Discount:   50000,
		Total:      0,
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: 10000,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm1, Weight: 1},
				{MemberID: tm2, Weight: 1},
			},
		}},
	}

	if _, err := usecase.CalculateFloorAllocation(input); !errors.Is(err, domain.ErrDiscountNotAllocatable) {
		t.Fatalf("mong đợi ErrDiscountNotAllocatable, nhận %v", err)
	}
}

func TestFloorAllocation_MissingCreditor_Rejected(t *testing.T) {
	// covers: AC-6 (không có Creditor thì không có người hấp thụ dư, phải từ chối)
	input := usecase.AllocationInput{
		Subtotal: 100,
		Total:    100,
		Items: []usecase.ItemInput{{
			ID:          uuid.New(),
			LineTotal:   100,
			Assignments: []usecase.ItemAssignmentInput{{MemberID: tm2, Weight: 1}},
		}},
	}

	if _, err := usecase.CalculateFloorAllocation(input); !errors.Is(err, domain.ErrCreditorRequired) {
		t.Fatalf("mong đợi ErrCreditorRequired, nhận %v", err)
	}
}

func TestFloorAllocation_DraftMismatch_NoDiscrepancyDumping(t *testing.T) {
	// covers: AC-6 (Total khai báo lệch với tổng các món thì khoản lệch không rơi lên đầu ai)
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   100000,
		Total:      500000, // OCR đọc 500.000 nhưng chỉ nhập 100.000 tiền món
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: 100000,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm1, Weight: 1},
				{MemberID: tm2, Weight: 1},
			},
		}},
	}

	res, err := usecase.CalculateFloorAllocation(input)
	if err != nil {
		t.Fatalf("CalculateFloorAllocation() error = %v", err)
	}
	if res[0].FinalAmount != 50000 || res[1].FinalAmount != 50000 {
		t.Errorf("mong đợi 50000/50000, nhận m1=%d, m2=%d", res[0].FinalAmount, res[1].FinalAmount)
	}
	if res[0].RoundingAdjustment != 0 || res[1].RoundingAdjustment != 0 {
		t.Errorf("mong đợi adjustment 0, nhận m1=%d, m2=%d", res[0].RoundingAdjustment, res[1].RoundingAdjustment)
	}
}

func TestFloorAllocation_LargeMonetaryAmount_NoOverflow(t *testing.T) {
	// covers: AC-6, AC-14 (số nguyên 64 bit trên hóa đơn 10 tỷ VND)
	const lineTotal = int64(10_000_000_001)
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   lineTotal,
		Total:      lineTotal,
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: lineTotal,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm1, Weight: 10000},
				{MemberID: tm2, Weight: 10000},
				{MemberID: tm3, Weight: 10000},
			},
		}},
	}

	res, err := usecase.CalculateFloorAllocation(input)
	if err != nil {
		t.Fatalf("CalculateFloorAllocation() error = %v", err)
	}

	var sum int64
	for _, a := range res {
		sum += a.FinalAmount
	}
	if sum != lineTotal {
		t.Errorf("mong đợi tổng đúng %d, nhận %d", lineTotal, sum)
	}
	// 10.000.000.001 chia 3 bằng 3.333.333.333 mỗi người, dư 2 đồng về Creditor tm1.
	if res[0].FinalAmount != 3333333335 || res[1].FinalAmount != 3333333333 || res[2].FinalAmount != 3333333333 {
		t.Errorf("phân bổ số lớn sai: m1=%d, m2=%d, m3=%d",
			res[0].FinalAmount, res[1].FinalAmount, res[2].FinalAmount)
	}
}

func TestFloorAllocation_FallbackRatio_Input(t *testing.T) {
	// covers: AC-6 (đầu vào dạng Ratio số thực quy về cùng thang trọng số)
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   100000,
		Total:      100000,
		Items: []usecase.ItemInput{{
			ID:        uuid.New(),
			LineTotal: 100000,
			Assignments: []usecase.ItemAssignmentInput{
				{MemberID: tm1, Ratio: 0.5},
				{MemberID: tm2, Ratio: 0.5},
			},
		}},
	}

	res, err := usecase.CalculateFloorAllocation(input)
	if err != nil {
		t.Fatalf("CalculateFloorAllocation() error = %v", err)
	}
	if res[0].FinalAmount != 50000 || res[1].FinalAmount != 50000 {
		t.Errorf("mong đợi 50000/50000, nhận m1=%d, m2=%d", res[0].FinalAmount, res[1].FinalAmount)
	}
}

// TestFloorAllocation_BruteForce_Invariants quét nhiều tổ hợp đầu vào và khẳng định ba bất biến.
// Lỗi tổng vượt hóa đơn của thuật toán Hamilton cũ lọt lưới đúng vì bộ test chỉ kiểm tra từng ca lẻ.
func TestFloorAllocation_BruteForce_Invariants(t *testing.T) {
	// covers: AC-6, AC-10
	members := []uuid.UUID{tm1, tm2, tm3}
	lineTotals := []int64{1, 3, 7, 100, 99999, 1000003}
	weightSets := [][]int64{{1, 1, 1}, {2, 1, 1}, {9900, 99, 1}, {1, 0, 0}, {5, 5, 1}}
	fees := []int64{0, 1, 7, 5000}
	discounts := []int64{0, 1, 999, 50000}

	var checked int
	for _, lt := range lineTotals {
		for _, ws := range weightSets {
			for _, sc := range fees {
				for _, vat := range fees {
					for _, disc := range discounts {
						for creditorIdx := 0; creditorIdx < len(members); creditorIdx++ {
							assignments := make([]usecase.ItemAssignmentInput, 0, len(members))
							for i, m := range members {
								if ws[i] > 0 {
									assignments = append(assignments, usecase.ItemAssignmentInput{MemberID: m, Weight: ws[i]})
								}
							}

							expectedTotal := lt + sc + vat - disc
							if expectedTotal < 0 {
								// Đầu vào không hợp lệ về mặt nghiệp vụ, tầng đối soát đã chặn từ trước.
								continue
							}

							in := usecase.AllocationInput{
								CreditorID:    members[creditorIdx],
								Subtotal:      lt,
								ServiceCharge: sc,
								VAT:           vat,
								Discount:      disc,
								Total:         expectedTotal,
								Members:       members,
								Items: []usecase.ItemInput{{
									ID:          uuid.New(),
									LineTotal:   lt,
									Assignments: assignments,
								}},
							}

							res, err := usecase.CalculateFloorAllocation(in)
							if err != nil {
								// Chỉ hai lỗi nghiệp vụ được phép xuất hiện ở đây.
								if errors.Is(err, domain.ErrDiscountNotAllocatable) {
									continue
								}
								t.Fatalf("lỗi ngoài dự kiến với lineTotal=%d ws=%v sc=%d vat=%d disc=%d creditor=%d: %v",
									lt, ws, sc, vat, disc, creditorIdx, err)
							}
							checked++

							expected := expectedTotal
							var sum int64
							var adjHolders int
							for _, a := range res {
								if a.FinalAmount < 0 {
									t.Fatalf("số âm với lineTotal=%d ws=%v sc=%d vat=%d disc=%d: thành viên %s nhận %d",
										lt, ws, sc, vat, disc, a.MemberID, a.FinalAmount)
								}
								sum += a.FinalAmount
								if a.RoundingAdjustment != 0 {
									adjHolders++
									if a.MemberID != members[creditorIdx] {
										t.Fatalf("adjustment khác 0 nằm ngoài Creditor: %s", a.MemberID)
									}
								}
							}
							if sum != expected {
								t.Fatalf("tổng lệch với lineTotal=%d ws=%v sc=%d vat=%d disc=%d: nhận %d, mong đợi %d",
									lt, ws, sc, vat, disc, sum, expected)
							}
							if adjHolders > 1 {
								t.Fatalf("có %d thành viên mang adjustment khác 0, chỉ được phép tối đa 1", adjHolders)
							}
						}
					}
				}
			}
		}
	}

	if checked < 500 {
		t.Fatalf("bộ quét quá nhỏ, chỉ kiểm tra được %d tổ hợp", checked)
	}
	t.Logf("đã kiểm tra %d tổ hợp", checked)
}
