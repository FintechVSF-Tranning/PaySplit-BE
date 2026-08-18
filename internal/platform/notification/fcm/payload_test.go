package fcm

import (
	"testing"
)

func TestPayloadBuilders(t *testing.T) {
	t.Run("PaymentReminder", func(t *testing.T) {
		msg := NewPaymentReminderMessage("Nam", 75000, "group-1", "bill-1")
		if msg.Title == "" || msg.Body == "" {
			t.Errorf("empty title or body: %+v", msg)
		}
		if msg.Data["type"] != TypePaymentReminder || msg.Data["group_id"] != "group-1" || msg.Data["bill_id"] != "bill-1" {
			t.Errorf("unexpected data: %+v", msg.Data)
		}
		if msg.Data["amount"] != "75000" {
			t.Errorf("unexpected amount: %s", msg.Data["amount"])
		}
	})

	t.Run("BillCreated", func(t *testing.T) {
		msg := NewBillCreatedMessage("Tín", "Pizza 4P's", "Hội Bạn", "group-1", "bill-2")
		if msg.Data["type"] != TypeNewBill || msg.Data["bill_id"] != "bill-2" {
			t.Errorf("unexpected data: %+v", msg.Data)
		}
	})

	t.Run("BillUpdated", func(t *testing.T) {
		msg := NewBillUpdatedMessage("Tín", "Pizza 4P's", "Hội Bạn", "group-1", "bill-2")
		if msg.Data["type"] != TypeBillUpdated || msg.Data["bill_id"] != "bill-2" {
			t.Errorf("unexpected data: %+v", msg.Data)
		}
	})

	t.Run("PaymentConfirmed", func(t *testing.T) {
		msg := NewPaymentConfirmedMessage("Lâm", 150000, "group-1", "pay-1")
		if msg.Data["type"] != TypePaymentConfirmed || msg.Data["payment_id"] != "pay-1" {
			t.Errorf("unexpected data: %+v", msg.Data)
		}
	})

	t.Run("PaymentRejected", func(t *testing.T) {
		msg := NewPaymentRejectedMessage("Lâm", "Chưa thấy tiền vào tài khoản", "group-1", "pay-1")
		if msg.Data["type"] != TypePaymentRejected || msg.Data["payment_id"] != "pay-1" {
			t.Errorf("unexpected data: %+v", msg.Data)
		}
	})

	t.Run("GroupInvitation", func(t *testing.T) {
		msg := NewGroupInvitationMessage("Nam", "Đi Phượt", "group-1", "INV123")
		if msg.Data["type"] != TypeGroupInvitation || msg.Data["invite_code"] != "INV123" {
			t.Errorf("unexpected data: %+v", msg.Data)
		}
	})

	t.Run("SystemAnnouncement", func(t *testing.T) {
		msg := NewSystemAnnouncementMessage("Bảo trì", "Hệ thống bảo trì 23h", map[string]string{"version": "1.0.0"})
		if msg.Data["type"] != TypeSystemAnnouncement || msg.Data["version"] != "1.0.0" {
			t.Errorf("unexpected data: %+v", msg.Data)
		}
	})
}

func TestFormatMoney(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1.000"},
		{50000, "50.000"},
		{1500000, "1.500.000"},
		{100000000, "100.000.000"},
	}

	for _, tc := range tests {
		result := formatMoney(tc.input)
		if result != tc.expected {
			t.Errorf("formatMoney(%d) = %s, expected %s", tc.input, result, tc.expected)
		}
	}
}
