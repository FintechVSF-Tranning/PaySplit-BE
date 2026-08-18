package usecase

import (
	"bytes"
	"errors"
	"math"
	"sort"

	"github.com/google/uuid"
)

// MemberAllocation chứa chi tiết phân bổ nợ cho một thành viên theo Spec 3 (Hamilton Method).
type MemberAllocation struct {
	MemberID           uuid.UUID `json:"member_id"`
	ItemSubtotal       int64     `json:"item_subtotal"`
	ServiceChargeShare int64     `json:"service_charge_share"`
	VATShare           int64     `json:"vat_share"`
	DiscountShare      int64     `json:"discount_share"`
	FinalAmount        int64     `json:"final_amount"`
}

// ItemInput chứa thông tin món ăn và tỷ lệ chia để tính toán phân bổ.
type ItemInput struct {
	ID          uuid.UUID
	LineTotal   int64
	Assignments []ItemAssignmentInput
}

// ItemAssignmentInput chứa thành viên và tỷ lệ gánh món (tổng tỷ lệ của 1 item phải = 1.0).
type ItemAssignmentInput struct {
	MemberID uuid.UUID
	Ratio    float64
}

// AllocationInput chứa toàn bộ dữ liệu đầu vào để phân bổ nợ hóa đơn.
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

// CalculateHamiltonAllocation tính toán phân bổ nợ theo giải thuật Hamilton Largest Remainder Method (Spec 3 AC-6, AC-9).
func CalculateHamiltonAllocation(in AllocationInput) ([]*MemberAllocation, error) {
	if in.Total < 0 || in.Subtotal < 0 || in.ServiceCharge < 0 || in.VAT < 0 || in.Discount < 0 {
		return nil, errors.New("monetary values must be non-negative")
	}

	// 1. Tập hợp danh sách tất cả các thành viên tham gia (bao gồm cả Creditor và assignees)
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

	// Sắp xếp danh sách thành viên theo thứ tự 16-byte UUID chuẩn (deterministic tie-breaking)
	sortUUIDs(allMembers)

	// Khởi tạo map phân bổ
	allocMap := make(map[uuid.UUID]*MemberAllocation, len(allMembers))
	for _, m := range allMembers {
		allocMap[m] = &MemberAllocation{
			MemberID: m,
		}
	}

	// 2. Bước 1: Phân bổ từng món ăn (Item-level Hamilton allocation)
	for _, it := range in.Items {
		if it.LineTotal <= 0 || len(it.Assignments) == 0 {
			continue
		}

		type memberShare struct {
			memberID  uuid.UUID
			floorVal  int64
			remainder float64
		}

		shares := make([]memberShare, 0, len(it.Assignments))
		var sumFloor int64

		for _, a := range it.Assignments {
			exact := float64(it.LineTotal) * a.Ratio
			floor := int64(math.Floor(exact))
			rem := exact - float64(floor)

			shares = append(shares, memberShare{
				memberID:  a.MemberID,
				floorVal:  floor,
				remainder: rem,
			})
			sumFloor += floor
		}

		undistributed := it.LineTotal - sumFloor

		// Sắp xếp theo remainder giảm dần; nếu bằng nhau thì theo thứ tự UUID byte tăng dần
		sort.SliceStable(shares, func(i, j int) bool {
			if math.Abs(shares[i].remainder-shares[j].remainder) > 1e-9 {
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

	// 3. Bước 2: Phân bổ Service Charge, VAT và Discount theo tỷ lệ ItemSubtotal
	var totalItemSubtotal int64
	for _, a := range allocMap {
		totalItemSubtotal += a.ItemSubtotal
	}

	if totalItemSubtotal > 0 {
		// Hamilton allocation cho ServiceCharge
		if in.ServiceCharge > 0 {
			scShares := runHamiltonForTotal(in.ServiceCharge, totalItemSubtotal, allMembers, allocMap)
			for m, v := range scShares {
				allocMap[m].ServiceChargeShare = v
			}
		}

		// Hamilton allocation cho VAT
		if in.VAT > 0 {
			vatShares := runHamiltonForTotal(in.VAT, totalItemSubtotal, allMembers, allocMap)
			for m, v := range vatShares {
				allocMap[m].VATShare = v
			}
		}

		// Hamilton allocation cho Discount
		if in.Discount > 0 {
			discShares := runHamiltonForTotal(in.Discount, totalItemSubtotal, allMembers, allocMap)
			for m, v := range discShares {
				allocMap[m].DiscountShare = v
			}
		}
	} else if in.CreditorID != uuid.Nil {
		// Nếu không có món ăn nào (subtotal = 0), toàn bộ thuế/phí/giảm giá quy về Creditor
		allocMap[in.CreditorID].ServiceChargeShare = in.ServiceCharge
		allocMap[in.CreditorID].VATShare = in.VAT
		allocMap[in.CreditorID].DiscountShare = in.Discount
	}

	// 4. Bước 3: Tính FinalAmount cho từng thành viên và kiểm tra tổng
	result := make([]*MemberAllocation, 0, len(allMembers))
	var computedTotalSum int64

	for _, m := range allMembers {
		a := allocMap[m]
		a.FinalAmount = a.ItemSubtotal + a.ServiceChargeShare + a.VATShare - a.DiscountShare
		if a.FinalAmount < 0 {
			a.FinalAmount = 0
		}
		computedTotalSum += a.FinalAmount
		result = append(result, a)
	}

	// Nếu có độ lệch nhỏ do discount/rounding thì người có FinalAmount lớn nhất (hoặc Creditor) sẽ gánh
	if computedTotalSum != in.Total && len(result) > 0 {
		diff := in.Total - computedTotalSum
		result[0].FinalAmount += diff
	}

	return result, nil
}

func runHamiltonForTotal(
	targetTotal int64,
	totalBase int64,
	members []uuid.UUID,
	allocMap map[uuid.UUID]*MemberAllocation,
) map[uuid.UUID]int64 {
	type memberShare struct {
		memberID  uuid.UUID
		floorVal  int64
		remainder float64
	}

	shares := make([]memberShare, 0, len(members))
	var sumFloor int64

	for _, m := range members {
		base := allocMap[m].ItemSubtotal
		exact := float64(targetTotal) * (float64(base) / float64(totalBase))
		floor := int64(math.Floor(exact))
		rem := exact - float64(floor)

		shares = append(shares, memberShare{
			memberID:  m,
			floorVal:  floor,
			remainder: rem,
		})
		sumFloor += floor
	}

	undistributed := targetTotal - sumFloor

	// Sắp xếp theo remainder giảm dần; nếu bằng nhau thì theo UUID byte tăng dần
	sort.SliceStable(shares, func(i, j int) bool {
		if math.Abs(shares[i].remainder-shares[j].remainder) > 1e-9 {
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

	return out
}

func sortUUIDs(uuids []uuid.UUID) {
	sort.Slice(uuids, func(i, j int) bool {
		return bytes.Compare(uuids[i][:], uuids[j][:]) < 0
	})
}
