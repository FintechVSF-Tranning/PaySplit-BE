package usecase

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/domain"
)

// weightScale là thang duy nhất dùng cho mọi trọng số phân bổ. Ratio dạng số thực được nhân lên
// thang này, và giá trị mặc định khi thiếu cả Weight lẫn Ratio cũng nằm trên thang này, để mọi
// nhánh của getWeight so sánh được với nhau (Spec 3 AC-6).
const weightScale int64 = 100_000_000

// MemberAllocation chứa phần chia đã tính cho một thành viên.
//
// RoundingAdjustment bằng 0 với mọi thành viên không phải Creditor. Với Creditor nó bằng
// FinalAmount trừ tổng bốn thành phần sàn của chính họ, nên có thể âm khi phần dư giảm giá lấn át
// (Spec 3 AC-10).
type MemberAllocation struct {
	MemberID           uuid.UUID `json:"member_id"`
	ItemSubtotal       int64     `json:"item_subtotal"`
	ServiceChargeShare int64     `json:"service_charge_share"`
	VATShare           int64     `json:"vat_share"`
	DiscountShare      int64     `json:"discount_share"`
	RoundingAdjustment int64     `json:"rounding_adjustment"`
	FinalAmount        int64     `json:"final_amount"`
}

// ItemInput chứa thông tin món hàng và danh sách gán thành viên.
type ItemInput struct {
	ID          uuid.UUID
	LineTotal   int64
	Assignments []ItemAssignmentInput
}

// ItemAssignmentInput chứa thành viên và trọng số cho một món hàng.
type ItemAssignmentInput struct {
	MemberID uuid.UUID
	Weight   int64
	Ratio    float64 // Hỗ trợ đầu vào dạng tỷ lệ số thực
}

// getWeight trả về trọng số của một lượt gán, luôn trên thang weightScale.
func (a ItemAssignmentInput) getWeight() int64 {
	if a.Weight > 0 {
		return a.Weight
	}
	if a.Ratio > 0 {
		scaled := int64(math.Round(a.Ratio * float64(weightScale)))
		if scaled > 0 {
			return scaled
		}
	}
	return weightScale
}

// AllocationInput chứa mọi đầu vào cần thiết để tính phân bổ.
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

// CalculateFloorAllocation tính phần chia của từng thành viên bằng số nguyên VND, theo thuật toán
// chia sàn và dồn phần dư cho Creditor (Spec 3 AC-6, AC-10).
//
// Thuật toán chạy hai lượt tách bạch:
//
//	Lượt một, mọi thành viên không phải Creditor. Mỗi thành phần tiền (món hàng, phí dịch vụ, VAT,
//	giảm giá) được chia sàn độc lập. FinalAmount là tiền hàng cộng phí cộng VAT trừ giảm giá. Nếu
//	giá trị đó âm, phần giảm giá của chính thành viên đó bị chặn lại để FinalAmount đúng bằng 0.
//	Mỗi người chỉ dựa vào số của chính mình nên lượt này không phụ thuộc thứ tự.
//
//	Lượt hai, Creditor. FinalAmount bằng tổng tính được trừ tổng FinalAmount của mọi người còn lại,
//	trong đó tổng tính được là tiền các món cộng phí cộng VAT trừ giảm giá, chứ không phải Total
//	khai báo trên bản nháp. Tính sau
//	khi mọi lần chặn trần ở lượt một đã xong. Nhờ vậy tổng khớp tuyệt đối theo cấu trúc, không phụ
//	thuộc vào việc phần dư của từng thành phần cộng lại có đẹp hay không.
//
// FinalAmount âm của Creditor không bao giờ bị kẹp về 0. Nó trả về
// domain.ErrDiscountNotAllocatable, vì kẹp chính là lỗi đã làm tổng vượt quá hóa đơn trước đây.
func CalculateFloorAllocation(in AllocationInput) ([]*MemberAllocation, error) {
	if in.Total < 0 || in.Subtotal < 0 || in.ServiceCharge < 0 || in.VAT < 0 || in.Discount < 0 {
		return nil, errors.New("monetary values must be non-negative")
	}
	// Phân bổ bắt buộc phải có Creditor. Không có người hấp thụ dư thứ hai, nên hóa đơn chưa có
	// Creditor là chưa sẵn sàng để tính (Spec 3 AC-6, bất biến 11).
	if in.CreditorID == uuid.Nil {
		return nil, domain.ErrCreditorRequired
	}

	// 1. Gom mọi thành viên tham gia. Creditor luôn có mặt kể cả khi không được gán món nào.
	memberSet := make(map[uuid.UUID]struct{})
	memberSet[in.CreditorID] = struct{}{}
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
	// Thứ tự byte UUID chuẩn, để kết quả tất định giữa Go và PostgreSQL.
	sortUUIDs(allMembers)

	allocMap := make(map[uuid.UUID]*MemberAllocation, len(allMembers))
	for _, m := range allMembers {
		allocMap[m] = &MemberAllocation{MemberID: m}
	}

	// 2. Chia sàn tiền từng món theo trọng số. Phần dư của món không chia lẻ.
	var itemsTotal int64
	for _, it := range in.Items {
		if it.LineTotal <= 0 || len(it.Assignments) == 0 {
			continue
		}
		itemsTotal += it.LineTotal

		var totalWeight int64
		for _, a := range it.Assignments {
			if w := a.getWeight(); w > 0 {
				totalWeight += w
			}
		}
		if totalWeight <= 0 {
			totalWeight = int64(len(it.Assignments))
		}

		for _, a := range it.Assignments {
			w := a.getWeight()
			if w <= 0 {
				w = 1
			}
			allocMap[a.MemberID].ItemSubtotal += (it.LineTotal * w) / totalWeight
		}
	}

	// 3. Chia sàn phí dịch vụ, VAT và giảm giá theo tỷ lệ tiền hàng của mỗi người.
	var totalItemSubtotal int64
	for _, a := range allocMap {
		totalItemSubtotal += a.ItemSubtotal
	}

	// Tổng dùng để phân bổ được tính từ chính các thành phần, không lấy in.Total khai báo. Bản nháp
	// có thể lưu Total lệch với tổng các món (OCR đọc sai, người dùng nhập thiếu), và khoản lệch đó
	// không được phép rơi lên đầu Creditor. Tầng đối soát báo lệch bằng mã mismatch riêng, còn phân
	// bổ luôn chia đúng số tiền thật của các món (Spec 3 AC-6).
	allocTotal := itemsTotal + in.ServiceCharge + in.VAT - in.Discount
	if allocTotal < 0 {
		return nil, domain.ErrDiscountNotAllocatable
	}

	if totalItemSubtotal > 0 {
		for _, m := range allMembers {
			base := allocMap[m].ItemSubtotal
			allocMap[m].ServiceChargeShare = floorShare(in.ServiceCharge, base, totalItemSubtotal)
			allocMap[m].VATShare = floorShare(in.VAT, base, totalItemSubtotal)
			allocMap[m].DiscountShare = floorShare(in.Discount, base, totalItemSubtotal)
		}
	} else {
		// Tiền hàng bằng 0: Creditor gánh toàn bộ phí, VAT và giảm giá. Nhánh này tính thẳng và
		// không đi qua vòng chặn trần, vì không có thành viên nào khác để chặn (Spec 3 AC-10).
		c := allocMap[in.CreditorID]
		c.ServiceChargeShare = in.ServiceCharge
		c.VATShare = in.VAT
		c.DiscountShare = in.Discount
	}

	// 4. Lượt một: mọi thành viên không phải Creditor.
	var sumOthers int64
	for _, m := range allMembers {
		if m == in.CreditorID {
			continue
		}
		a := allocMap[m]
		owed := a.ItemSubtotal + a.ServiceChargeShare + a.VATShare
		// Chặn trần giảm giá bằng đúng số tiền người đó phải trả, thay vì kẹp kết quả cuối về 0.
		// Phần bị cắt rơi về Creditor một cách tự nhiên nhờ phép trừ ngược ở lượt hai.
		if a.DiscountShare > owed {
			a.DiscountShare = owed
		}
		a.FinalAmount = owed - a.DiscountShare
		a.RoundingAdjustment = 0
		sumOthers += a.FinalAmount
	}

	// 5. Lượt hai: Creditor nhận phần còn lại, nên tổng khớp theo cấu trúc.
	c := allocMap[in.CreditorID]
	c.FinalAmount = allocTotal - sumOthers
	if c.FinalAmount < 0 {
		// Giảm giá hợp lệ về tổng nhưng dồn vào những người không hấp thụ nổi, nên phần bị cắt đẩy
		// Creditor xuống âm. Từ chối chứ không kẹp (Spec 3 AC-10).
		return nil, domain.ErrDiscountNotAllocatable
	}
	c.RoundingAdjustment = c.FinalAmount - (c.ItemSubtotal + c.ServiceChargeShare + c.VATShare - c.DiscountShare)

	// 6. Bất biến cuối cùng. Nếu một thay đổi sau này phá vỡ nó, lỗi hiện ra ở đây thay vì âm thầm
	// tạo ra khoản nợ không có tiền thật đứng sau.
	result := make([]*MemberAllocation, 0, len(allMembers))
	var sumFinal int64
	for _, m := range allMembers {
		a := allocMap[m]
		if a.FinalAmount < 0 {
			return nil, fmt.Errorf("allocation invariant broken: member %s has negative final amount %d", m, a.FinalAmount)
		}
		sumFinal += a.FinalAmount
		result = append(result, a)
	}
	if sumFinal != allocTotal {
		return nil, fmt.Errorf("allocation invariant broken: shares sum to %d but computed bill total is %d", sumFinal, allocTotal)
	}

	return result, nil
}

// floorShare trả về phần sàn của targetTotal theo tỷ lệ base trên totalBase.
func floorShare(targetTotal, base, totalBase int64) int64 {
	if targetTotal <= 0 || base <= 0 || totalBase <= 0 {
		return 0
	}
	return (targetTotal * base) / totalBase
}

func sortUUIDs(uuids []uuid.UUID) {
	sort.Slice(uuids, func(i, j int) bool {
		return bytes.Compare(uuids[i][:], uuids[j][:]) < 0
	})
}
