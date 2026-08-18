package usecase_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/usecase"
)

func TestHamilton_EqualSplit_ExactTotalSum(t *testing.T) {
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
					{MemberID: m1, Ratio: 1.0 / 3.0},
					{MemberID: m2, Ratio: 1.0 / 3.0},
					{MemberID: m3, Ratio: 1.0 / 3.0},
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

	// m1 có byte UUID nhỏ nhất -> nhận 33,334đ, m2 và m3 nhận 33,333đ
	if res[0].FinalAmount != 33334 || res[1].FinalAmount != 33333 || res[2].FinalAmount != 33333 {
		t.Errorf("unexpected distribution: m1=%d, m2=%d, m3=%d", res[0].FinalAmount, res[1].FinalAmount, res[2].FinalAmount)
	}
}

func TestHamilton_VAT_ServiceCharge_Discount(t *testing.T) {
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
					{MemberID: m1, Ratio: 1.0},
				},
			},
			{
				ID:        uuid.New(),
				LineTotal: 100000,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m2, Ratio: 1.0},
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
					{MemberID: u2, Ratio: 0.5},
					{MemberID: u1, Ratio: 0.5},
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	// Total 100,001 divided evenly = 50,000.5 each.
	// Both have remainder 0.5. Ascending UUID means u1 gets the extra 1 VND -> 50,001 VND. u2 gets 50,000 VND.
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
	// covers: AC-6, AC-10 (Discount clamping and rounding diff balancing never produce negative FinalAmount)
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
					{MemberID: m1, Ratio: 0.99},
					{MemberID: m2, Ratio: 0.01},
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
func TestHamilton_100Items_50Members_PerformanceAndExactness(t *testing.T) {
	// covers: AC-6, AC-10, AC-14 (Scale: 100 items, 50 members, sub-50ms execution, exact sum reconciliation)
	members := make([]uuid.UUID, 50)
	for i := 0; i < 50; i++ {
		members[i] = uuid.New()
	}
	creditor := members[0]

	items := make([]usecase.ItemInput, 100)
	var subtotal int64
	for i := 0; i < 100; i++ {
		lineTotal := int64((i + 1) * 15000)
		subtotal += lineTotal

		// Assign item to 5 members with equal ratio
		assignees := make([]usecase.ItemAssignmentInput, 5)
		for j := 0; j < 5; j++ {
			assignees[j] = usecase.ItemAssignmentInput{
				MemberID: members[(i+j)%50],
				Ratio:    0.20,
			}
		}

		items[i] = usecase.ItemInput{
			ID:          uuid.New(),
			LineTotal:   lineTotal,
			Assignments: assignees,
		}
	}

	serviceCharge := int64(subtotal / 20) // 5%
	vat := int64(subtotal / 10)           // 10%
	discount := int64(subtotal / 25)      // 4%
	total := subtotal + serviceCharge + vat - discount

	input := usecase.AllocationInput{
		CreditorID:    creditor,
		Subtotal:      subtotal,
		ServiceCharge: serviceCharge,
		VAT:           vat,
		Discount:      discount,
		Total:         total,
		Items:         items,
		Members:       members,
	}

	start := time.Now()
	res, err := usecase.CalculateHamiltonAllocation(input)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	// Performance target: < 50ms per Spec 3 AC-14
	if duration > 50*time.Millisecond {
		t.Errorf("expected execution time < 50ms, got %v", duration)
	}

	// Exact sum check
	var sum int64
	for _, a := range res {
		if a.FinalAmount < 0 {
			t.Errorf("member %s got negative amount %d", a.MemberID, a.FinalAmount)
		}
		sum += a.FinalAmount
	}

	if sum != total {
		t.Errorf("expected total sum %d, got %d (diff: %d)", total, sum, total-sum)
	}
}

func TestHamilton_DeterministicAscendingUUIDTieBreaking_MultipleMembers(t *testing.T) {
	// covers: AC-6, AC-10 (Deterministic tie breaking with 4 members having identical remainder)
	m1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	m2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	m3 := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	m4 := uuid.MustParse("00000000-0000-0000-0000-000000000004")

	// Line total = 100001 divided by 4 = 25000.25 each
	// Remainder for all 4 is 0.25. Undistributed = 100001 - 100000 = 1 VND.
	// m1 has lowest UUID byte -> m1 gets the +1 VND -> 25001 VND. m2, m3, m4 get 25000 VND.
	input := usecase.AllocationInput{
		CreditorID: m1,
		Subtotal:   100001,
		Total:      100001,
		Items: []usecase.ItemInput{
			{
				ID:        uuid.New(),
				LineTotal: 100001,
				Assignments: []usecase.ItemAssignmentInput{
					{MemberID: m4, Ratio: 0.25},
					{MemberID: m3, Ratio: 0.25},
					{MemberID: m2, Ratio: 0.25},
					{MemberID: m1, Ratio: 0.25},
				},
			},
		},
	}

	res, err := usecase.CalculateHamiltonAllocation(input)
	if err != nil {
		t.Fatalf("CalculateHamiltonAllocation() error = %v", err)
	}

	allocMap := make(map[uuid.UUID]int64)
	for _, a := range res {
		allocMap[a.MemberID] = a.FinalAmount
	}

	if allocMap[m1] != 25001 {
		t.Errorf("expected m1 to get 25001 VND due to ascending UUID tie breaking, got %d", allocMap[m1])
	}
	if allocMap[m2] != 25000 || allocMap[m3] != 25000 || allocMap[m4] != 25000 {
		t.Errorf("expected m2, m3, m4 to get 25000 VND, got m2=%d, m3=%d, m4=%d", allocMap[m2], allocMap[m3], allocMap[m4])
	}
}
