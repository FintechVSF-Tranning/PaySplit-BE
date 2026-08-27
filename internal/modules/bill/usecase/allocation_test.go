package usecase_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/usecase"
)

var (
	tm1 = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	tm2 = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	tm3 = uuid.MustParse("00000000-0000-0000-0000-000000000003")
	tm4 = uuid.MustParse("00000000-0000-0000-0000-000000000004")
	tm5 = uuid.MustParse("00000000-0000-0000-0000-000000000005")
	tm6 = uuid.MustParse("00000000-0000-0000-0000-000000000006")
)

func TestAllocation_AggregatesExactItemSharesBeforeRounding(t *testing.T) {
	// covers: AC-6, AC-10
	members := []uuid.UUID{tm1, tm2, tm3, tm4, tm5, tm6}
	assignments := equalAssignments(members)
	input := usecase.AllocationInput{
		CreditorID: tm6,
		Subtotal:   1_200_000,
		Total:      1_200_000,
		Items: []usecase.ItemInput{
			{ID: uuid.New(), LineTotal: 400_000, Assignments: assignments},
			{ID: uuid.New(), LineTotal: 800_000, Assignments: assignments},
		},
	}

	result := mustAllocate(t, input)
	for _, allocation := range result {
		if allocation.ItemSubtotal != 200_000 || allocation.FinalAmount != 200_000 || allocation.RoundingAdjustment != 0 {
			t.Fatalf("member %s received subtotal=%d adjustment=%d final=%d, want 200000/0/200000",
				allocation.MemberID, allocation.ItemSubtotal, allocation.RoundingAdjustment, allocation.FinalAmount)
		}
	}
	assertAllocationInvariants(t, result, 1_200_000)
}

func TestAllocation_LargestRemainderUsesUUIDTieBreakNotCreditor(t *testing.T) {
	// covers: AC-6, AC-10
	input := usecase.AllocationInput{
		CreditorID: tm3,
		Subtotal:   100_000,
		Total:      100_000,
		Items: []usecase.ItemInput{{
			ID:          uuid.New(),
			LineTotal:   100_000,
			Assignments: equalAssignments([]uuid.UUID{tm3, tm2, tm1}),
		}},
	}

	result := allocationByMember(mustAllocate(t, input))
	if result[tm1].FinalAmount != 33_334 || result[tm2].FinalAmount != 33_333 || result[tm3].FinalAmount != 33_333 {
		t.Fatalf("unexpected tie result: tm1=%d tm2=%d tm3=%d",
			result[tm1].FinalAmount, result[tm2].FinalAmount, result[tm3].FinalAmount)
	}
	if result[tm1].RoundingAdjustment != 1 || result[tm3].RoundingAdjustment != 0 {
		t.Fatalf("remainder must follow UUID, got tm1 adjustment=%d creditor adjustment=%d",
			result[tm1].RoundingAdjustment, result[tm3].RoundingAdjustment)
	}
}

func TestAllocation_MixedParticipantsAggregateExactly(t *testing.T) {
	// covers: AC-6, AC-10
	input := usecase.AllocationInput{
		CreditorID: tm3,
		Subtotal:   150_000,
		Total:      150_000,
		Items: []usecase.ItemInput{
			{ID: uuid.New(), LineTotal: 100_000, Assignments: equalAssignments([]uuid.UUID{tm1, tm2, tm3})},
			{ID: uuid.New(), LineTotal: 50_000, Assignments: equalAssignments([]uuid.UUID{tm1, tm2})},
		},
	}

	result := allocationByMember(mustAllocate(t, input))
	if result[tm1].FinalAmount != 58_334 || result[tm2].FinalAmount != 58_333 || result[tm3].FinalAmount != 33_333 {
		t.Fatalf("unexpected mixed allocation: tm1=%d tm2=%d tm3=%d",
			result[tm1].FinalAmount, result[tm2].FinalAmount, result[tm3].FinalAmount)
	}
}

func TestAllocation_PrivateItemStaysWithParticipant(t *testing.T) {
	// covers: AC-6
	input := usecase.AllocationInput{
		CreditorID: tm3,
		Subtotal:   190_000,
		Total:      190_000,
		Items: []usecase.ItemInput{
			{ID: uuid.New(), LineTotal: 90_000, Assignments: []usecase.ItemAssignmentInput{{MemberID: tm1, Weight: 1}}},
			{ID: uuid.New(), LineTotal: 100_000, Assignments: equalAssignments([]uuid.UUID{tm1, tm2, tm3})},
		},
	}

	result := allocationByMember(mustAllocate(t, input))
	if result[tm1].FinalAmount != 123_334 || result[tm2].FinalAmount != 33_333 || result[tm3].FinalAmount != 33_333 {
		t.Fatalf("unexpected private item allocation: tm1=%d tm2=%d tm3=%d",
			result[tm1].FinalAmount, result[tm2].FinalAmount, result[tm3].FinalAmount)
	}
}

func TestAllocation_InputOrderDoesNotChangeResult(t *testing.T) {
	// covers: AC-6, AC-10
	item1 := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	item2 := uuid.MustParse("10000000-0000-0000-0000-000000000002")
	forward := usecase.AllocationInput{
		CreditorID: tm3,
		Subtotal:   150_000,
		Total:      150_000,
		Members:    []uuid.UUID{tm1, tm2, tm3},
		Items: []usecase.ItemInput{
			{ID: item1, LineTotal: 100_000, Assignments: equalAssignments([]uuid.UUID{tm1, tm2, tm3})},
			{ID: item2, LineTotal: 50_000, Assignments: equalAssignments([]uuid.UUID{tm1, tm2})},
		},
	}
	reversed := usecase.AllocationInput{
		CreditorID: tm3,
		Subtotal:   150_000,
		Total:      150_000,
		Members:    []uuid.UUID{tm3, tm2, tm1},
		Items: []usecase.ItemInput{
			{ID: item2, LineTotal: 50_000, Assignments: equalAssignments([]uuid.UUID{tm2, tm1})},
			{ID: item1, LineTotal: 100_000, Assignments: equalAssignments([]uuid.UUID{tm3, tm2, tm1})},
		},
	}

	if got, want := allocationByMember(mustAllocate(t, reversed)), allocationByMember(mustAllocate(t, forward)); !reflect.DeepEqual(got, want) {
		t.Fatalf("allocation changed with input order:\ngot=%+v\nwant=%+v", got, want)
	}
}

func TestAllocation_ServiceChargeVATAndDiscount(t *testing.T) {
	// covers: AC-6, AC-10
	input := usecase.AllocationInput{
		CreditorID:    tm2,
		Subtotal:      100_000,
		ServiceCharge: 5_000,
		VAT:           10_000,
		Discount:      3_000,
		Total:         112_000,
		Items: []usecase.ItemInput{{
			ID: uuid.New(), LineTotal: 100_000,
			Assignments: equalAssignments([]uuid.UUID{tm1, tm2}),
		}},
	}

	result := mustAllocate(t, input)
	for _, allocation := range result {
		if allocation.ServiceChargeShare != 2_500 || allocation.VATShare != 5_000 || allocation.DiscountShare != 1_500 || allocation.FinalAmount != 56_000 {
			t.Fatalf("unexpected components for %s: %+v", allocation.MemberID, allocation)
		}
	}
	assertAllocationInvariants(t, result, 112_000)
}

func TestAllocation_ZeroSubtotalCreditorBearsBillComponents(t *testing.T) {
	// covers: AC-10
	input := usecase.AllocationInput{
		CreditorID:    tm1,
		ServiceCharge: 5_000,
		VAT:           2_000,
		Discount:      1_000,
		Total:         6_000,
		Members:       []uuid.UUID{tm1, tm2},
	}

	result := allocationByMember(mustAllocate(t, input))
	if result[tm1].FinalAmount != 6_000 || result[tm2].FinalAmount != 0 {
		t.Fatalf("unexpected zero subtotal allocation: tm1=%d tm2=%d", result[tm1].FinalAmount, result[tm2].FinalAmount)
	}
}

func TestAllocation_LargeAmountUsesExactArithmetic(t *testing.T) {
	// covers: AC-6, AC-14
	const lineTotal = int64(9_000_000_000_000_000_001)
	input := usecase.AllocationInput{
		CreditorID: tm3,
		Subtotal:   lineTotal,
		Total:      lineTotal,
		Items: []usecase.ItemInput{{
			ID:          uuid.New(),
			LineTotal:   lineTotal,
			Assignments: equalAssignments([]uuid.UUID{tm1, tm2, tm3}),
		}},
	}

	result := allocationByMember(mustAllocate(t, input))
	if result[tm1].FinalAmount != 3_000_000_000_000_000_001 || result[tm2].FinalAmount != 3_000_000_000_000_000_000 || result[tm3].FinalAmount != 3_000_000_000_000_000_000 {
		t.Fatalf("unexpected large allocation: tm1=%d tm2=%d tm3=%d",
			result[tm1].FinalAmount, result[tm2].FinalAmount, result[tm3].FinalAmount)
	}
}

func TestAllocation_DraftTotalMismatchIsNotAllocated(t *testing.T) {
	// covers: AC-6
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   100_000,
		Total:      500_000,
		Items: []usecase.ItemInput{{
			ID: uuid.New(), LineTotal: 100_000,
			Assignments: equalAssignments([]uuid.UUID{tm1, tm2}),
		}},
	}

	result := allocationByMember(mustAllocate(t, input))
	if result[tm1].FinalAmount != 50_000 || result[tm2].FinalAmount != 50_000 {
		t.Fatalf("declared mismatch leaked into allocation: tm1=%d tm2=%d", result[tm1].FinalAmount, result[tm2].FinalAmount)
	}
}

func TestAllocation_Validation(t *testing.T) {
	// covers: AC-6, AC-10
	validItem := func() usecase.ItemInput {
		return usecase.ItemInput{ID: uuid.New(), LineTotal: 100, Assignments: []usecase.ItemAssignmentInput{{MemberID: tm1, Weight: 1}}}
	}
	tests := []struct {
		name  string
		input usecase.AllocationInput
		want  error
	}{
		{name: "missing creditor", input: usecase.AllocationInput{Items: []usecase.ItemInput{validItem()}}, want: domain.ErrCreditorRequired},
		{name: "unassigned item", input: usecase.AllocationInput{CreditorID: tm1, Items: []usecase.ItemInput{{ID: uuid.New(), LineTotal: 100}}}, want: nil},
		{name: "zero weight", input: usecase.AllocationInput{CreditorID: tm1, Items: []usecase.ItemInput{{ID: uuid.New(), LineTotal: 100, Assignments: []usecase.ItemAssignmentInput{{MemberID: tm1}}}}}, want: nil},
		{name: "duplicate member on item", input: usecase.AllocationInput{CreditorID: tm1, Items: []usecase.ItemInput{{ID: uuid.New(), LineTotal: 100, Assignments: []usecase.ItemAssignmentInput{{MemberID: tm1, Weight: 1}, {MemberID: tm1, Weight: 1}}}}}, want: nil},
		{name: "negative money", input: usecase.AllocationInput{CreditorID: tm1, Discount: -1, Items: []usecase.ItemInput{validItem()}}, want: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := usecase.CalculateAllocation(test.input)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestAllocation_DiscountExceedsComputedBillIsRejected(t *testing.T) {
	// covers: AC-10
	input := usecase.AllocationInput{
		CreditorID: tm1,
		Subtotal:   10_000,
		Discount:   50_000,
		Items: []usecase.ItemInput{{
			ID: uuid.New(), LineTotal: 10_000,
			Assignments: equalAssignments([]uuid.UUID{tm1, tm2}),
		}},
	}
	if _, err := usecase.CalculateAllocation(input); !errors.Is(err, domain.ErrDiscountNotAllocatable) {
		t.Fatalf("got %v, want ErrDiscountNotAllocatable", err)
	}
}

func TestAllocation_BruteForceInvariants(t *testing.T) {
	// covers: AC-6, AC-10
	members := []uuid.UUID{tm1, tm2, tm3}
	lineTotals := []int64{1, 3, 7, 100, 99_999, 1_000_003}
	weightSets := [][]int64{{1, 1, 1}, {2, 1, 1}, {9900, 99, 1}, {5, 5, 1}}
	fees := []int64{0, 1, 7, 5_000}
	discounts := []int64{0, 1, 999, 50_000}

	var checked int
	for _, lineTotal := range lineTotals {
		for _, weights := range weightSets {
			for _, service := range fees {
				for _, vat := range fees {
					for _, discount := range discounts {
						expected := lineTotal + service + vat - discount
						if expected < 0 {
							continue
						}
						assignments := make([]usecase.ItemAssignmentInput, len(members))
						for i, memberID := range members {
							assignments[i] = usecase.ItemAssignmentInput{MemberID: memberID, Weight: weights[i]}
						}
						for _, creditorID := range members {
							input := usecase.AllocationInput{
								CreditorID: creditorID, Subtotal: lineTotal, ServiceCharge: service,
								VAT: vat, Discount: discount, Total: expected, Members: members,
								Items: []usecase.ItemInput{{ID: uuid.New(), LineTotal: lineTotal, Assignments: assignments}},
							}
							result, err := usecase.CalculateAllocation(input)
							if err != nil {
								if errors.Is(err, domain.ErrDiscountNotAllocatable) {
									continue
								}
								t.Fatalf("unexpected error for total=%d weights=%v service=%d vat=%d discount=%d: %v",
									lineTotal, weights, service, vat, discount, err)
							}
							checked++
							assertAllocationInvariants(t, result, expected)
						}
					}
				}
			}
		}
	}
	if checked < 500 {
		t.Fatalf("brute force sweep too small: %d", checked)
	}
}

func BenchmarkAllocation_100Items50Members(b *testing.B) {
	// covers: AC-14
	members := make([]uuid.UUID, 50)
	for i := range members {
		members[i] = uuid.MustParse("00000000-0000-0000-0000-" + fmt.Sprintf("%012d", i+1))
	}
	items := make([]usecase.ItemInput, 100)
	var total int64
	for itemIndex := range items {
		lineTotal := int64(1_000_000 + itemIndex)
		total += lineTotal
		assignments := make([]usecase.ItemAssignmentInput, len(members))
		for memberIndex, memberID := range members {
			assignments[memberIndex] = usecase.ItemAssignmentInput{MemberID: memberID, Weight: int64(memberIndex + 1)}
		}
		items[itemIndex] = usecase.ItemInput{ID: uuid.New(), LineTotal: lineTotal, Assignments: assignments}
	}
	input := usecase.AllocationInput{CreditorID: members[49], Subtotal: total, Total: total, Members: members, Items: items}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := usecase.CalculateAllocation(input); err != nil {
			b.Fatal(err)
		}
	}
}

func equalAssignments(members []uuid.UUID) []usecase.ItemAssignmentInput {
	result := make([]usecase.ItemAssignmentInput, len(members))
	for i, memberID := range members {
		result[i] = usecase.ItemAssignmentInput{MemberID: memberID, Weight: 1}
	}
	return result
}

func mustAllocate(t *testing.T, input usecase.AllocationInput) []*usecase.MemberAllocation {
	t.Helper()
	result, err := usecase.CalculateAllocation(input)
	if err != nil {
		t.Fatalf("CalculateAllocation() error = %v", err)
	}
	return result
}

func allocationByMember(allocations []*usecase.MemberAllocation) map[uuid.UUID]usecase.MemberAllocation {
	result := make(map[uuid.UUID]usecase.MemberAllocation, len(allocations))
	for _, allocation := range allocations {
		result[allocation.MemberID] = *allocation
	}
	return result
}

func assertAllocationInvariants(t *testing.T, allocations []*usecase.MemberAllocation, expectedTotal int64) {
	t.Helper()
	var sum int64
	for _, allocation := range allocations {
		if allocation.FinalAmount < 0 {
			t.Fatalf("member %s has negative final amount %d", allocation.MemberID, allocation.FinalAmount)
		}
		componentTotal := allocation.ItemSubtotal + allocation.ServiceChargeShare + allocation.VATShare - allocation.DiscountShare + allocation.RoundingAdjustment
		if componentTotal != allocation.FinalAmount {
			t.Fatalf("member %s component equation=%d final=%d", allocation.MemberID, componentTotal, allocation.FinalAmount)
		}
		sum += allocation.FinalAmount
	}
	if sum != expectedTotal {
		t.Fatalf("allocation sum=%d, want %d", sum, expectedTotal)
	}
}
