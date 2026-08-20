package usecase_test

import (
	"testing"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/usecase"
)

func TestHamilton_EqualSplit_ExactTotalSum(t *testing.T) {
	// covers: AC-6, AC-10 (Equal split with exact integer Hamilton allocation and sum-to-total conservation)
	m1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	m2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	m3 := uuid.MustParse("00000000-0000-0000-0000-000000000003")

	itemID := uuid.New()
	input := usecase.AllocationInput{
		CreditorID:    m1,
		Subtotal:      100000,
		ServiceCharge: 0,
		VAT:           0,
		Discount:      0,
		Total:         100000,
		Items: []usecase.ItemInput{
			{
				ID:        itemID,
				LineTotal: 100000,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m1, Weight: 1},
					{MemberID: m2, Weight: 1},
					{MemberID: m3, Weight: 1},
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	if len(res) != 3 {
		t.Fatalf("expected 3 members, got %d", len(res))
	}

	var sum int64
	for _, a := range res {
		sum += a.FinalAmount
	}

	if sum != 100000 {
		t.Errorf("expected total sum 100000 VND, got %d VND", sum)
	}

	// m1 has lowest UUID byte order -> receives 33,334đ; m2 and m3 receive 33,333đ
	if res[0].FinalAmount != 33334 || res[1].FinalAmount != 33333 || res[2].FinalAmount != 33333 {
		t.Errorf("unexpected distribution: m1=%d, m2=%d, m3=%d", res[0].FinalAmount, res[1].FinalAmount, res[2].FinalAmount)
	}
}

func TestHamilton_IntegerWeights_ProportionalSplit(t *testing.T) {
	// covers: AC-6, AC-10 (Integer weights proportional split 2/3 and 1/3)
	m1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	m2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	input := usecase.AllocationInput{
		CreditorID:    m1,
		Subtotal:      100000,
		ServiceCharge: 0,
		VAT:           0,
		Discount:      0,
		Total:         100000,
		Items: []usecase.ItemInput{
			{
				ID:        uuid.New(),
				LineTotal: 100000,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m1, Weight: 2}, // 2 shares = 2/3
					{MemberID: m2, Weight: 1}, // 1 share = 1/3
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	// 100,000 * 2/3 = 66,666.666... (floor 66666, rem 2)
	// 100,000 * 1/3 = 33,333.333... (floor 33333, rem 1)
	// Remainder 2 > 1, so m1 gets the +1 VND -> 66,667 VND; m2 gets 33,333 VND.
	if res[0].FinalAmount != 66667 || res[1].FinalAmount != 33333 {
		t.Errorf("expected m1=66667, m2=33333; got m1=%d, m2=%d", res[0].FinalAmount, res[1].FinalAmount)
	}
}

func TestHamilton_VAT_ServiceCharge_Discount(t *testing.T) {
	// covers: AC-6, AC-9, AC-10 (Service charge, VAT, and Discount distribution)
	m1 := uuid.New()
	m2 := uuid.New()

	input := usecase.AllocationInput{
		CreditorID:    m1,
		Subtotal:      200000,
		ServiceCharge: 10000,
		VAT:           20000,
		Discount:      15000,
		Total:         215000,
		Items: []usecase.ItemInput{
			{
				ID:        uuid.New(),
				LineTotal: 100000,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m1, Weight: 10000},
				},
			},
			{
				ID:        uuid.New(),
				LineTotal: 100000,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m2, Weight: 10000},
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	var sum int64
	for _, a := range res {
		sum += a.FinalAmount
	}

	if sum != 215000 {
		t.Errorf("expected total sum 215000 VND, got %d VND", sum)
	}
}

func TestHamilton_ZeroSubtotal_CreditorBearsFees(t *testing.T) {
	// covers: AC-10 (If subtotal is zero, all service charge and VAT belong to the Creditor)
	creditor := uuid.New()
	member := uuid.New()

	input := usecase.AllocationInput{
		CreditorID:    creditor,
		Subtotal:      0,
		ServiceCharge: 15000,
		VAT:           10000,
		Discount:      0,
		Total:         25000,
		Items:         []usecase.ItemInput{},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	if len(res) != 1 {
		t.Fatalf("expected only creditor in allocation, got %d", len(res))
	}
	if res[0].MemberID != creditor {
		t.Errorf("expected creditor %s, got %s", creditor, res[0].MemberID)
	}
	if res[0].FinalAmount != 25000 {
		t.Errorf("expected creditor to bear full 25000, got %d", res[0].FinalAmount)
	}
	_ = member
}

func TestHamilton_DeterministicUUIDTieBreaking(t *testing.T) {
	// covers: AC-6, AC-10 (Ascending UUID tie breaking)
	u1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	u2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	input := usecase.AllocationInput{
		CreditorID: u1,
		Subtotal:   100001,
		Total:      100001,
		Items: []usecase.ItemInput{
			{
				ID:        uuid.New(),
				LineTotal: 100001,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: u2, Weight: 5000},
					{MemberID: u1, Weight: 5000},
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	// Total 100,001 divided evenly = 50,000.5 each.
	// Both have remainder 5000. Ascending UUID means u1 gets the extra 1 VND -> 50,001 VND. u2 gets 50,000 VND.
	var a1, a2 int64
	for _, a := range res {
		if a.MemberID == u1 {
			a1 = a.FinalAmount
		}
		if a.MemberID == u2 {
			a2 = a.FinalAmount
		}
	}

	if a1 != 50001 || a2 != 50000 {
		t.Errorf("expected u1=50001, u2=50000, got u1=%d, u2=%d", a1, a2)
	}
}

func TestHamilton_LargeDiscount_NeverProducesNegativeFinalAmount(t *testing.T) {
	// covers: AC-6, AC-10 (Discount clamping never produces negative FinalAmount)
	m1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	m2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	input := usecase.AllocationInput{
		CreditorID:    m1,
		Subtotal:      100000,
		ServiceCharge: 0,
		VAT:           0,
		Discount:      99999,
		Total:         1,
		Items: []usecase.ItemInput{
			{
				ID:        uuid.New(),
				LineTotal: 100000,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m1, Weight: 9900},
					{MemberID: m2, Weight: 100},
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	var sum int64
	for _, a := range res {
		if a.FinalAmount < 0 {
			t.Errorf("member %s received negative final amount: %d", a.MemberID, a.FinalAmount)
		}
		sum += a.FinalAmount
	}

	if sum != 1 {
		t.Errorf("expected total sum = 1, got %d", sum)
	}
}

func TestHamilton_DraftMismatch_NoDiscrepancyDumping(t *testing.T) {
	// covers: AC-6 (Preview allocation does not dump draft total discrepancies onto members)
	m1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	m2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	input := usecase.AllocationInput{
		CreditorID:    m1,
		Subtotal:      100000,
		ServiceCharge: 0,
		VAT:           0,
		Discount:      0,
		Total:         500000, // OCR read 500,000, but only 100,000 of items entered
		Items: []usecase.ItemInput{
			{
				ID:        uuid.New(),
				LineTotal: 100000,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m1, Weight: 1},
					{MemberID: m2, Weight: 1},
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	// Each member should be allocated exactly their fair share of the 100,000 VND items (50,000 each)
	// and neither member should be burdened with the 400,000 VND discrepancy.
	if res[0].FinalAmount != 50000 || res[1].FinalAmount != 50000 {
		t.Errorf("expected clean item split 50000/50000, got m1=%d, m2=%d", res[0].FinalAmount, res[1].FinalAmount)
	}
	if res[0].RoundingAdjustment != 0 || res[1].RoundingAdjustment != 0 {
		t.Errorf("expected zero rounding adjustment, got m1=%d, m2=%d", res[0].RoundingAdjustment, res[1].RoundingAdjustment)
	}
}

func TestHamilton_LargeMonetaryAmount_NoOverflow(t *testing.T) {
	// covers: AC-6, AC-14 (Checked 64-bit integer arithmetic without overflow on 10 billion VND bills)
	m1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	m2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	m3 := uuid.MustParse("00000000-0000-0000-0000-000000000003")

	const lineTotal = int64(10_000_000_001) // 10 billion + 1 VND
	input := usecase.AllocationInput{
		CreditorID:    m1,
		Subtotal:      lineTotal,
		ServiceCharge: 0,
		VAT:           0,
		Discount:      0,
		Total:         lineTotal,
		Items: []usecase.ItemInput{
			{
				ID:        uuid.New(),
				LineTotal: lineTotal,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m1, Weight: 10000},
					{MemberID: m2, Weight: 10000},
					{MemberID: m3, Weight: 10000},
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	var sum int64
	for _, a := range res {
		sum += a.FinalAmount
	}

	if sum != lineTotal {
		t.Errorf("expected exact sum %d, got %d", lineTotal, sum)
	}

	// 10,000,000,001 / 3 = 3,333,333,333 remainder 2
	// m1 and m2 get +1 VND -> 3,333,333,334; m3 gets 3,333,333,333
	if res[0].FinalAmount != 3333333334 || res[1].FinalAmount != 3333333334 || res[2].FinalAmount != 3333333333 {
		t.Errorf("unexpected large amount distribution: m1=%d, m2=%d, m3=%d", res[0].FinalAmount, res[1].FinalAmount, res[2].FinalAmount)
	}
}

func TestHamilton_FallbackRatio_Input(t *testing.T) {
	// covers: AC-6 (Fallback support for legacy Ratio float field converted to integer)
	m1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	m2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	input := usecase.AllocationInput{
		CreditorID: m1,
		Subtotal:   100000,
		Total:      100000,
		Items: []usecase.ItemInput{
			{
				ID:        uuid.New(),
				LineTotal: 100000,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m1, Ratio: 0.5},
					{MemberID: m2, Ratio: 0.5},
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	if res[0].FinalAmount != 50000 || res[1].FinalAmount != 50000 {
		t.Errorf("expected 50000/50000, got m1=%d, m2=%d", res[0].FinalAmount, res[1].FinalAmount)
	}
}
