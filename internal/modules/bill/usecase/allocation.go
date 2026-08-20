package usecase

import (
	"bytes"
	"errors"
	"math"
	"sort"

	"github.com/google/uuid"
)

// MemberAllocation contains the calculated share for one member under the Hamilton Method.
type MemberAllocation struct {
	MemberID           uuid.UUID `json:"member_id"`
	ItemSubtotal       int64     `json:"item_subtotal"`
	ServiceChargeShare int64     `json:"service_charge_share"`
	VATShare           int64     `json:"vat_share"`
	DiscountShare      int64     `json:"discount_share"`
	RoundingAdjustment int64     `json:"rounding_adjustment"`
	FinalAmount        int64     `json:"final_amount"`
}

// ItemInput contains item details and member assignments for allocation.
type ItemInput struct {
	ID          uuid.UUID
	LineTotal   int64
	Assignments []ItemAssignmentInput
}

// ItemAssignmentInput contains member and weight for an item.
type ItemAssignmentInput struct {
	MemberID uuid.UUID
	Weight   int64
	Ratio    float64 // Fallback support for float ratio inputs
}

// getWeight returns the assignment weight as an integer.
func (a ItemAssignmentInput) getWeight() int64 {
	if a.Weight > 0 {
		return a.Weight
	}
	if a.Ratio > 0 {
		scaled := int64(math.Round(a.Ratio * 100000000))
		if scaled > 0 {
			return scaled
		}
	}
	return 10000
}

// AllocationInput contains all inputs required to calculate debt allocation.
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

// CalculateHamiltonAllocation calculates member shares using exact integer Hamilton Largest Remainder arithmetic.
func CalculateHamiltonAllocation(in AllocationInput) ([]*MemberAllocation, error) {
	if in.Total < 0 || in.Subtotal < 0 || in.ServiceCharge < 0 || in.VAT < 0 || in.Discount < 0 {
		return nil, errors.New("monetary values must be non-negative")
	}

	// 1. Collect all participating members (Creditor, explicit members, and item assignees)
	memberSet := make(map[uuid.UUID]struct{})
	if in.CreditorID != uuid.Nil {
		memberSet[in.CreditorID] = struct{}{}
	}
	for _, m := range in.Members {
		memberSet[m] = struct{}{}
	}
	for _, it := range in.Items {
		for _, a := range it.Assignments {
			memberSet[a.MemberID] = struct{}{}
		}
	}

	allMembers := make([]uuid.UUID, 0, len(memberSet))
	for m := range memberSet {
		allMembers = append(allMembers, m)
	}

	// Sort members by canonical 16 byte UUID order for deterministic tie breaking
	sortUUIDs(allMembers)

	allocMap := make(map[uuid.UUID]*MemberAllocation, len(allMembers))
	itemFloorMap := make(map[uuid.UUID]int64, len(allMembers))
	scFloorMap := make(map[uuid.UUID]int64, len(allMembers))
	vatFloorMap := make(map[uuid.UUID]int64, len(allMembers))
	discFloorMap := make(map[uuid.UUID]int64, len(allMembers))

	for _, m := range allMembers {
		allocMap[m] = &MemberAllocation{
			MemberID: m,
		}
	}

	// 2. Step 1: Item level exact integer Hamilton allocation
	for _, it := range in.Items {
		if it.LineTotal <= 0 || len(it.Assignments) == 0 {
			continue
		}

		var totalWeight int64
		for _, a := range it.Assignments {
			w := a.getWeight()
			if w > 0 {
				totalWeight += w
			}
		}
		if totalWeight <= 0 {
			totalWeight = int64(len(it.Assignments))
		}

		type memberShare struct {
			memberID  uuid.UUID
			floorVal  int64
			remainder int64
		}

		shares := make([]memberShare, 0, len(it.Assignments))
		var sumFloor int64

		for _, a := range it.Assignments {
			w := a.getWeight()
			if w <= 0 {
				w = 1
			}

			floor := (it.LineTotal * w) / totalWeight
			rem := (it.LineTotal * w) % totalWeight

			shares = append(shares, memberShare{
				memberID:  a.MemberID,
				floorVal:  floor,
				remainder: rem,
			})
			sumFloor += floor
			itemFloorMap[a.MemberID] += floor
		}

		undistributed := it.LineTotal - sumFloor

		// Sort shares by remainder descending; break ties by ascending UUID byte order
		sort.SliceStable(shares, func(i, j int) bool {
			if shares[i].remainder != shares[j].remainder {
				return shares[i].remainder > shares[j].remainder
			}
			return bytes.Compare(shares[i].memberID[:], shares[j].memberID[:]) < 0
		})

		for i := 0; i < len(shares); i++ {
			add := int64(0)
			if int64(i) < undistributed {
				add = 1
			}
			allocMap[shares[i].memberID].ItemSubtotal += shares[i].floorVal + add
		}
	}

	// 3. Step 2: Allocate Service Charge, VAT, and Discount proportionally by ItemSubtotal
	var totalItemSubtotal int64
	for _, a := range allocMap {
		totalItemSubtotal += a.ItemSubtotal
	}

	if totalItemSubtotal > 0 {
		if in.ServiceCharge > 0 {
			scShares, scFloors := runHamiltonForTotal(in.ServiceCharge, totalItemSubtotal, allMembers, allocMap)
			for m, v := range scShares {
				allocMap[m].ServiceChargeShare = v
				scFloorMap[m] = scFloors[m]
			}
		}

		if in.VAT > 0 {
			vatShares, vatFloors := runHamiltonForTotal(in.VAT, totalItemSubtotal, allMembers, allocMap)
			for m, v := range vatShares {
				allocMap[m].VATShare = v
				vatFloorMap[m] = vatFloors[m]
			}
		}

		if in.Discount > 0 {
			discShares, discFloors := runHamiltonForTotal(in.Discount, totalItemSubtotal, allMembers, allocMap)
			for m, v := range discShares {
				allocMap[m].DiscountShare = v
				discFloorMap[m] = discFloors[m]
			}
		}
	} else if in.CreditorID != uuid.Nil {
		// When subtotal is zero, creditor bears fees and discount (Spec 3 AC-10)
		allocMap[in.CreditorID].ServiceChargeShare = in.ServiceCharge
		allocMap[in.CreditorID].VATShare = in.VAT
		allocMap[in.CreditorID].DiscountShare = in.Discount
		scFloorMap[in.CreditorID] = in.ServiceCharge
		vatFloorMap[in.CreditorID] = in.VAT
		discFloorMap[in.CreditorID] = in.Discount
	}

	// 4. Step 3: Compute RoundingAdjustment and FinalAmount for each member
	result := make([]*MemberAllocation, 0, len(allMembers))
	for _, m := range allMembers {
		a := allocMap[m]

		itemAdj := a.ItemSubtotal - itemFloorMap[m]
		scAdj := a.ServiceChargeShare - scFloorMap[m]
		vatAdj := a.VATShare - vatFloorMap[m]
		discAdj := a.DiscountShare - discFloorMap[m]
		a.RoundingAdjustment = itemAdj + scAdj + vatAdj - discAdj

		a.FinalAmount = a.ItemSubtotal + a.ServiceChargeShare + a.VATShare - a.DiscountShare
		if a.FinalAmount < 0 {
			a.FinalAmount = 0
		}
		result = append(result, a)
	}

	return result, nil
}

func runHamiltonForTotal(
	targetTotal int64,
	totalBase int64,
	members []uuid.UUID,
	allocMap map[uuid.UUID]*MemberAllocation,
) (map[uuid.UUID]int64, map[uuid.UUID]int64) {
	type memberShare struct {
		memberID  uuid.UUID
		floorVal  int64
		remainder int64
	}

	shares := make([]memberShare, 0, len(members))
	floors := make(map[uuid.UUID]int64, len(members))
	var sumFloor int64

	for _, m := range members {
		base := allocMap[m].ItemSubtotal
		floor := (targetTotal * base) / totalBase
		rem := (targetTotal * base) % totalBase

		shares = append(shares, memberShare{
			memberID:  m,
			floorVal:  floor,
			remainder: rem,
		})
		floors[m] = floor
		sumFloor += floor
	}

	undistributed := targetTotal - sumFloor

	// Sort shares by remainder descending; break ties by ascending UUID byte order
	sort.SliceStable(shares, func(i, j int) bool {
		if shares[i].remainder != shares[j].remainder {
			return shares[i].remainder > shares[j].remainder
		}
		return bytes.Compare(shares[i].memberID[:], shares[j].memberID[:]) < 0
	})

	out := make(map[uuid.UUID]int64, len(members))
	for i := 0; i < len(shares); i++ {
		add := int64(0)
		if int64(i) < undistributed {
			add = 1
		}
		out[shares[i].memberID] = shares[i].floorVal + add
	}

	return out, floors
}

func sortUUIDs(uuids []uuid.UUID) {
	sort.Slice(uuids, func(i, j int) bool {
		return bytes.Compare(uuids[i][:], uuids[j][:]) < 0
	})
}
