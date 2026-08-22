package http

import (
	"strconv"
	"time"

	"paysplit-backend/internal/modules/settlement/domain"
)

type expenseItemResponse struct {
	ItemName           string `json:"item_name"`
	Quantity           string `json:"quantity"`
	UnitPrice          string `json:"unit_price"`
	LineTotal          string `json:"line_total"`
	ItemDiscountAmount string `json:"item_discount_amount"`
	ItemFinalPrice     string `json:"item_final_price"`
	ShareRatio         string `json:"share_ratio"`
	ItemShare          string `json:"item_share"`
}
type allocationResponse struct {
	ItemSubtotal       string `json:"item_subtotal"`
	ServiceChargeShare string `json:"service_charge_share"`
	VATShare           string `json:"vat_share"`
	DiscountShare      string `json:"discount_share"`
	RoundingAdjustment string `json:"rounding_adjustment"`
	FinalAmount        string `json:"final_amount"`
}
type expenseBillResponse struct {
	BillID              string                `json:"bill_id"`
	BillDate            string                `json:"bill_date"`
	MerchantName        string                `json:"merchant_name"`
	CreditorMemberID    string                `json:"creditor_member_id"`
	CreditorDisplayName string                `json:"creditor_display_name"`
	DebtID              *string               `json:"debt_id"`
	DebtStatus          *string               `json:"debt_status"`
	Allocation          allocationResponse    `json:"allocation"`
	Items               []expenseItemResponse `json:"items"`
}

func expensePageResponse(page *domain.ExpensePage) map[string]any {
	bills := make([]expenseBillResponse, 0)
	byID := make(map[string]int)
	itemSubtotals := make(map[string]int64)
	for _, row := range page.Items {
		idx, ok := byID[row.BillID]
		if !ok {
			var status *string
			if row.DebtStatus != "" {
				v := row.DebtStatus
				status = &v
			}
			idx = len(bills)
			byID[row.BillID] = idx
			bills = append(bills, expenseBillResponse{BillID: row.BillID, BillDate: row.BillDate.Format("2006-01-02"), MerchantName: row.MerchantName,
				CreditorMemberID: row.CreditorMemberID, CreditorDisplayName: row.CreditorDisplayName, DebtID: row.DebtID, DebtStatus: status,
				Allocation: allocationResponse{"0", strconv.FormatInt(row.ServiceChargeShare, 10), strconv.FormatInt(row.VATShare, 10), strconv.FormatInt(row.DiscountShare, 10), strconv.FormatInt(row.RoundingAdjustment, 10), strconv.FormatInt(row.FinalAmount, 10)}, Items: []expenseItemResponse{}})
		}
		bills[idx].Items = append(bills[idx].Items, expenseItemResponse{row.ItemName, row.Quantity, strconv.FormatInt(row.UnitPrice, 10), strconv.FormatInt(row.LineTotal, 10), strconv.FormatInt(row.ItemDiscountAmount, 10), strconv.FormatInt(row.ItemFinalPrice, 10), row.ShareRatio, strconv.FormatInt(row.ItemShare, 10)})
		itemSubtotals[row.BillID] += row.ItemShare
		bills[idx].Allocation.ItemSubtotal = strconv.FormatInt(itemSubtotals[row.BillID], 10)
	}
	return map[string]any{"summary": map[string]string{"total_owed": strconv.FormatInt(page.Summary.TotalOwed, 10), "total_settled": strconv.FormatInt(page.Summary.TotalSettled, 10), "total_receivable": strconv.FormatInt(page.Summary.TotalReceivable, 10), "net_balance": strconv.FormatInt(page.Summary.NetBalance, 10)}, "bills": bills, "next_cursor": page.NextCursor}
}

type debtResponse struct {
	ID                  string     `json:"id"`
	BillID              string     `json:"bill_id"`
	BillDate            string     `json:"bill_date"`
	MerchantName        string     `json:"merchant_name"`
	DebtorMemberID      string     `json:"debtor_member_id"`
	DebtorDisplayName   string     `json:"debtor_display_name"`
	DebtorAvatarURL     *string    `json:"debtor_avatar_url"`
	CreditorMemberID    string     `json:"creditor_member_id"`
	CreditorDisplayName string     `json:"creditor_display_name"`
	CreditorAvatarURL   *string    `json:"creditor_avatar_url"`
	Amount              string     `json:"amount"`
	Status              string     `json:"status"`
	ReminderCount       int32      `json:"reminder_count"`
	PaymentID           *string    `json:"payment_id"`
	CreatedAt           time.Time  `json:"created_at"`
	SettledAt           *time.Time `json:"settled_at"`
}

func (h *Handler) debtPageResponse(page *domain.DebtPage) map[string]any {
	items := make([]debtResponse, 0, len(page.Debts))
	for _, d := range page.Debts {
		var da, ca *string
		if d.DebtorAvatarObjectKey != nil {
			v := h.avatarURL(*d.DebtorAvatarObjectKey)
			da = &v
		}
		if d.CreditorAvatarObjectKey != nil {
			v := h.avatarURL(*d.CreditorAvatarObjectKey)
			ca = &v
		}
		items = append(items, debtResponse{d.ID, d.BillID, d.BillDate.Format("2006-01-02"), d.MerchantName, d.DebtorMemberID, d.DebtorDisplayName, da, d.CreditorMemberID, d.CreditorDisplayName, ca, strconv.FormatInt(d.Amount, 10), d.Status, d.ReminderCount, d.PaymentID, d.CreatedAt, d.SettledAt})
	}
	matrix := make([]map[string]any, 0, len(page.NetMatrix))
	for _, m := range page.NetMatrix {
		matrix = append(matrix, map[string]any{"debtor_member_id": m.DebtorMemberID, "creditor_member_id": m.CreditorMemberID, "total_amount": strconv.FormatInt(m.TotalAmount, 10), "debt_count": m.DebtCount})
	}
	return map[string]any{"debts": items, "net_matrix": matrix, "caller_payable": strconv.FormatInt(page.CallerPayable, 10), "caller_receivable": strconv.FormatInt(page.CallerReceivable, 10), "next_cursor": page.NextCursor}
}

func paymentResponse(p *domain.Payment) map[string]any {
	var recipient any
	if p.Recipient.AccountNumber != "" {
		recipient = map[string]string{"bank_code": p.Recipient.Code, "bank_name": p.Recipient.Name, "account_number": p.Recipient.AccountNumber, "account_holder": p.Recipient.AccountHolder}
	}
	nullable := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	coveredDebtIDs := p.CoveredDebtIDs
	if coveredDebtIDs == nil {
		coveredDebtIDs = []string{}
	}
	return map[string]any{"id": p.ID, "group_id": p.GroupID, "debtor_member_id": p.DebtorMemberID, "creditor_member_id": p.CreditorMemberID, "amount": strconv.FormatInt(p.Amount, 10), "reference_code": p.ReferenceCode, "status": p.Status, "qr_payload": nullable(p.QRPayload), "qr_image_url": nullable(p.QRImageURL), "recipient": recipient, "image_url": p.ImageURL, "note": p.Note, "rejection_reason": p.RejectionReason, "covered_debt_ids": coveredDebtIDs, "created_at": p.CreatedAt, "submitted_at": p.SubmittedAt, "confirmed_at": p.ConfirmedAt, "rejected_at": p.RejectedAt}
}
