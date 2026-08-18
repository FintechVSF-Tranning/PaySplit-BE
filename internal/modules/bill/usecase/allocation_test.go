package usecase_test

import (
	"testing"

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
