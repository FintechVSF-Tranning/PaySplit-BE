package usecase

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/domain"
)

const weightScale int64 = 100_000_000

// MemberAllocation chứa phần chia nguyên VND của một thành viên.
//
// RoundingAdjustment nối các thành phần nguyên với FinalAmount sau khi phân bổ phần dư:
//
//	FinalAmount = ItemSubtotal + ServiceChargeShare + VATShare - DiscountShare + RoundingAdjustment
//
// Adjustment có thể thuộc bất kỳ thành viên nào, Creditor không được ưu tiên (Spec 3 AC-6, AC-10).
type MemberAllocation struct {
	MemberID           uuid.UUID `json:"member_id"`
	ItemSubtotal       int64     `json:"item_subtotal"`
	ServiceChargeShare int64     `json:"service_charge_share"`
	VATShare           int64     `json:"vat_share"`
	DiscountShare      int64     `json:"discount_share"`
	RoundingAdjustment int64     `json:"rounding_adjustment"`
	FinalAmount        int64     `json:"final_amount"`
}

type ItemInput struct {
	ID          uuid.UUID
	LineTotal   int64
	Assignments []ItemAssignmentInput
}

type ItemAssignmentInput struct {
	MemberID uuid.UUID
	Weight   int64
}

type AllocationInput struct {
	CreditorID    uuid.UUID
	Subtotal      int64
	ServiceCharge int64
	VAT           int64
	Discount      int64
	Total         int64
	Items         []ItemInput
	Members       []uuid.UUID
}

type exactMemberAllocation struct {
	memberID uuid.UUID
	item     *big.Rat
	service  *big.Rat
	vat      *big.Rat
	discount *big.Rat
	final    *big.Rat
	base     int64
}

// CalculateAllocation cộng phần tiền chính xác của từng thành viên qua mọi món trước khi làm
// tròn. Phần VND còn lại được trao theo phần lẻ giảm dần, hòa thì UUID byte chuẩn tăng dần.
// Mọi phép tính trung gian dùng phân số chính xác, không dùng float cho tiền.
func CalculateAllocation(in AllocationInput) ([]*MemberAllocation, error) {
	if in.Total < 0 || in.Subtotal < 0 || in.ServiceCharge < 0 || in.VAT < 0 || in.Discount < 0 {
		return nil, errors.New("monetary values must be non-negative")
	}
	if in.CreditorID == uuid.Nil {
		return nil, domain.ErrCreditorRequired
	}

	memberSet := map[uuid.UUID]struct{}{in.CreditorID: struct{}{}}
	for _, memberID := range in.Members {
		if memberID == uuid.Nil {
			return nil, errors.New("member ID must not be nil")
		}
		memberSet[memberID] = struct{}{}
	}

	var itemsTotal int64
	exactItems := make(map[uuid.UUID]*big.Rat)
	for itemIndex, item := range in.Items {
		if item.LineTotal < 0 {
			return nil, fmt.Errorf("item %d line total must be non-negative", itemIndex)
		}
		if len(item.Assignments) == 0 {
			return nil, fmt.Errorf("item %d must have at least one assignment", itemIndex)
		}

		var err error
		itemsTotal, err = checkedAddInt64(itemsTotal, item.LineTotal)
		if err != nil {
			return nil, fmt.Errorf("items total overflow: %w", err)
		}

		totalWeight := new(big.Int)
		seenOnItem := make(map[uuid.UUID]struct{}, len(item.Assignments))
		for assignmentIndex, assignment := range item.Assignments {
			if assignment.MemberID == uuid.Nil {
				return nil, fmt.Errorf("item %d assignment %d has nil member ID", itemIndex, assignmentIndex)
			}
			if assignment.Weight <= 0 {
				return nil, fmt.Errorf("item %d assignment %d weight must be positive", itemIndex, assignmentIndex)
			}
			if _, duplicate := seenOnItem[assignment.MemberID]; duplicate {
				return nil, fmt.Errorf("item %d has duplicate member %s", itemIndex, assignment.MemberID)
			}
			seenOnItem[assignment.MemberID] = struct{}{}
			memberSet[assignment.MemberID] = struct{}{}
			totalWeight.Add(totalWeight, big.NewInt(assignment.Weight))
		}

		for _, assignment := range item.Assignments {
			numerator := new(big.Int).Mul(big.NewInt(item.LineTotal), big.NewInt(assignment.Weight))
			share := new(big.Rat).SetFrac(numerator, totalWeight)
			if exactItems[assignment.MemberID] == nil {
				exactItems[assignment.MemberID] = new(big.Rat)
			}
			exactItems[assignment.MemberID].Add(exactItems[assignment.MemberID], share)
		}
	}

	allocTotal, err := checkedAllocationTotal(itemsTotal, in.ServiceCharge, in.VAT, in.Discount)
	if err != nil {
		return nil, err
	}

	allMembers := make([]uuid.UUID, 0, len(memberSet))
	for memberID := range memberSet {
		allMembers = append(allMembers, memberID)
	}
	sortUUIDs(allMembers)

	exactByID := make(map[uuid.UUID]*exactMemberAllocation, len(allMembers))
	for _, memberID := range allMembers {
		itemShare := new(big.Rat)
		if exactItems[memberID] != nil {
			itemShare.Set(exactItems[memberID])
		}
		exactByID[memberID] = &exactMemberAllocation{
			memberID: memberID,
			item:     itemShare,
			service:  new(big.Rat),
			vat:      new(big.Rat),
			discount: new(big.Rat),
			final:    new(big.Rat),
		}
	}

	if itemsTotal > 0 {
		itemsTotalInt := big.NewInt(itemsTotal)
		for _, memberID := range allMembers {
			exact := exactByID[memberID]
			exact.service = proportionalRat(in.ServiceCharge, exact.item, itemsTotalInt)
			exact.vat = proportionalRat(in.VAT, exact.item, itemsTotalInt)
			exact.discount = proportionalRat(in.Discount, exact.item, itemsTotalInt)
		}
	} else {
		creditor := exactByID[in.CreditorID]
		creditor.service.SetInt64(in.ServiceCharge)
		creditor.vat.SetInt64(in.VAT)
		creditor.discount.SetInt64(in.Discount)
	}

	// Phần discount một thành viên không hấp thụ được chuyển sang Creditor vì nghiệp vụ discount
	// reconciliation, không phải vì làm tròn.
	creditor := exactByID[in.CreditorID]
	for _, memberID := range allMembers {
		if memberID == in.CreditorID {
			continue
		}
		exact := exactByID[memberID]
		owed := new(big.Rat).Add(new(big.Rat).Set(exact.item), exact.service)
		owed.Add(owed, exact.vat)
		if exact.discount.Cmp(owed) > 0 {
			excess := new(big.Rat).Sub(new(big.Rat).Set(exact.discount), owed)
			exact.discount.Set(owed)
			creditor.discount.Add(creditor.discount, excess)
		}
	}

	var sumBase int64
	for _, memberID := range allMembers {
		exact := exactByID[memberID]
		exact.final.Add(new(big.Rat).Set(exact.item), exact.service)
		exact.final.Add(exact.final, exact.vat)
		exact.final.Sub(exact.final, exact.discount)
		if exact.final.Sign() < 0 {
			if memberID == in.CreditorID {
				return nil, domain.ErrDiscountNotAllocatable
			}
			return nil, fmt.Errorf("allocation invariant broken: member %s has negative exact final amount", memberID)
		}
		exact.base, err = floorRatInt64(exact.final)
		if err != nil {
			return nil, fmt.Errorf("member %s final amount: %w", memberID, err)
		}
		sumBase, err = checkedAddInt64(sumBase, exact.base)
		if err != nil {
			return nil, fmt.Errorf("allocation base total overflow: %w", err)
		}
	}

	exactSum := new(big.Rat)
	for _, memberID := range allMembers {
		exactSum.Add(exactSum, exactByID[memberID].final)
	}
	if exactSum.Cmp(new(big.Rat).SetInt64(allocTotal)) != 0 {
		return nil, errors.New("allocation invariant broken: exact shares do not equal computed bill total")
	}

	remaining := allocTotal - sumBase
	if remaining < 0 || remaining >= int64(len(allMembers)) {
		return nil, fmt.Errorf("allocation invariant broken: invalid remaining amount %d", remaining)
	}

	orderedByRemainder := append([]uuid.UUID(nil), allMembers...)
	sort.SliceStable(orderedByRemainder, func(i, j int) bool {
		left := fractionalPart(exactByID[orderedByRemainder[i]].final)
		right := fractionalPart(exactByID[orderedByRemainder[j]].final)
		if cmp := left.Cmp(right); cmp != 0 {
			return cmp > 0
		}
		return bytes.Compare(orderedByRemainder[i][:], orderedByRemainder[j][:]) < 0
	})
	for i := int64(0); i < remaining; i++ {
		exactByID[orderedByRemainder[i]].base++
	}

	result := make([]*MemberAllocation, 0, len(allMembers))
	var sumFinal int64
	for _, memberID := range allMembers {
		exact := exactByID[memberID]
		itemSubtotal, err := floorRatInt64(exact.item)
		if err != nil {
			return nil, fmt.Errorf("member %s item subtotal: %w", memberID, err)
		}
		serviceShare, err := floorRatInt64(exact.service)
		if err != nil {
			return nil, fmt.Errorf("member %s service share: %w", memberID, err)
		}
		vatShare, err := floorRatInt64(exact.vat)
		if err != nil {
			return nil, fmt.Errorf("member %s VAT share: %w", memberID, err)
		}
		discountShare, err := floorRatInt64(exact.discount)
		if err != nil {
			return nil, fmt.Errorf("member %s discount share: %w", memberID, err)
		}

		componentTotal, err := checkedComponentTotal(itemSubtotal, serviceShare, vatShare, discountShare)
		if err != nil {
			return nil, fmt.Errorf("member %s component total: %w", memberID, err)
		}
		roundingAdjustment, err := checkedSubInt64(exact.base, componentTotal)
		if err != nil {
			return nil, fmt.Errorf("member %s rounding adjustment: %w", memberID, err)
		}

		allocation := &MemberAllocation{
			MemberID:           memberID,
			ItemSubtotal:       itemSubtotal,
			ServiceChargeShare: serviceShare,
			VATShare:           vatShare,
			DiscountShare:      discountShare,
			RoundingAdjustment: roundingAdjustment,
			FinalAmount:        exact.base,
		}
		sumFinal, err = checkedAddInt64(sumFinal, allocation.FinalAmount)
		if err != nil {
			return nil, fmt.Errorf("allocation final total overflow: %w", err)
		}
		result = append(result, allocation)
	}

	if sumFinal != allocTotal {
		return nil, fmt.Errorf("allocation invariant broken: shares sum to %d but computed bill total is %d", sumFinal, allocTotal)
	}
	return result, nil
}

func proportionalRat(total int64, base *big.Rat, totalBase *big.Int) *big.Rat {
	if total == 0 || base.Sign() == 0 || totalBase.Sign() == 0 {
		return new(big.Rat)
	}
	result := new(big.Rat).Mul(new(big.Rat).SetInt64(total), base)
	return result.Quo(result, new(big.Rat).SetInt(totalBase))
}

func floorRatInt64(value *big.Rat) (int64, error) {
	if value.Sign() < 0 {
		return 0, errors.New("cannot floor a negative amount")
	}
	quotient := new(big.Int).Quo(value.Num(), value.Denom())
	if !quotient.IsInt64() {
		return 0, errors.New("amount exceeds int64")
	}
	return quotient.Int64(), nil
}

func fractionalPart(value *big.Rat) *big.Rat {
	floor := new(big.Int).Quo(value.Num(), value.Denom())
	return new(big.Rat).Sub(value, new(big.Rat).SetInt(floor))
}

func checkedAllocationTotal(items, service, vat, discount int64) (int64, error) {
	total, err := checkedAddInt64(items, service)
	if err != nil {
		return 0, errors.New("computed bill total overflow")
	}
	total, err = checkedAddInt64(total, vat)
	if err != nil {
		return 0, errors.New("computed bill total overflow")
	}
	total, err = checkedSubInt64(total, discount)
	if err != nil || total < 0 {
		return 0, domain.ErrDiscountNotAllocatable
	}
	return total, nil
}

func checkedComponentTotal(items, service, vat, discount int64) (int64, error) {
	total, err := checkedAddInt64(items, service)
	if err != nil {
		return 0, errors.New("component total overflow")
	}
	total, err = checkedAddInt64(total, vat)
	if err != nil {
		return 0, errors.New("component total overflow")
	}
	total, err = checkedSubInt64(total, discount)
	if err != nil {
		return 0, errors.New("component total overflow")
	}
	return total, nil
}

func checkedAddInt64(left, right int64) (int64, error) {
	const maxInt64 = int64(^uint64(0) >> 1)
	const minInt64 = -maxInt64 - 1
	if right > 0 && left > maxInt64-right {
		return 0, errors.New("int64 addition overflow")
	}
	if right < 0 && left < minInt64-right {
		return 0, errors.New("int64 addition underflow")
	}
	return left + right, nil
}

func checkedSubInt64(left, right int64) (int64, error) {
	const minInt64 = -int64(^uint64(0)>>1) - 1
	if right == minInt64 {
		if left >= 0 {
			return 0, errors.New("int64 subtraction overflow")
		}
		return left - right, nil
	}
	return checkedAddInt64(left, -right)
}

func sortUUIDs(uuids []uuid.UUID) {
	sort.Slice(uuids, func(i, j int) bool {
		return bytes.Compare(uuids[i][:], uuids[j][:]) < 0
	})
}
