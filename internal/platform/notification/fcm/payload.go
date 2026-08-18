package fcm

import (
	"fmt"
	"strconv"
)

const (
	TypePaymentReminder    = "payment_reminder"
	TypeNewBill            = "new_bill"
	TypePaymentConfirmed   = "payment_confirmed"
	TypePaymentRejected    = "payment_rejected"
	TypeGroupInvitation    = "group_invitation"
	TypeBillUpdated        = "bill_updated"
	TypeSystemAnnouncement = "system_announcement"
)

// Action bắt sự kiện click thông báo mặc định cho Flutter
const DefaultFlutterClickAction = "FLUTTER_NOTIFICATION_CLICK"

// NewPaymentReminderMessage tạo thông báo khi một người nhắc thanh toán nợ
func NewPaymentReminderMessage(actorName string, amount int64, groupID, billID string) PushMessage {
	return PushMessage{
		Title: "Nhắc nhở thanh toán 💸",
		Body:  fmt.Sprintf("%s vừa nhắc bạn thanh toán %sđ.", actorName, formatMoney(amount)),
		Data: map[string]string{
			"type":         TypePaymentReminder,
			"group_id":     groupID,
			"bill_id":      billID,
			"amount":       strconv.FormatInt(amount, 10),
			"click_action": DefaultFlutterClickAction,
		},
	}
}

// NewBillCreatedMessage tạo thông báo khi có hóa đơn mới trong nhóm
func NewBillCreatedMessage(creatorName, billTitle, groupName, groupID, billID string) PushMessage {
	return PushMessage{
		Title: fmt.Sprintf("Hóa đơn mới trong '%s' 🍕", groupName),
		Body:  fmt.Sprintf("%s vừa thêm hóa đơn '%s'. Bấm để xem chi tiết.", creatorName, billTitle),
		Data: map[string]string{
			"type":         TypeNewBill,
			"group_id":     groupID,
			"bill_id":      billID,
			"click_action": DefaultFlutterClickAction,
		},
	}
}

// NewBillUpdatedMessage tạo thông báo khi hóa đơn trong nhóm bị chỉnh sửa
func NewBillUpdatedMessage(updaterName, billTitle, groupName, groupID, billID string) PushMessage {
	return PushMessage{
		Title: fmt.Sprintf("Cập nhật hóa đơn trong '%s' ✏️", groupName),
		Body:  fmt.Sprintf("%s vừa cập nhật hóa đơn '%s'.", updaterName, billTitle),
		Data: map[string]string{
			"type":         TypeBillUpdated,
			"group_id":     groupID,
			"bill_id":      billID,
			"click_action": DefaultFlutterClickAction,
		},
	}
}

// NewPaymentConfirmedMessage tạo thông báo khi chủ nợ xác nhận đã nhận được tiền
func NewPaymentConfirmedMessage(confirmerName string, amount int64, groupID, paymentID string) PushMessage {
	return PushMessage{
		Title: "Thanh toán thành công ✅",
		Body:  fmt.Sprintf("%s đã xác nhận nhận được %sđ từ bạn.", confirmerName, formatMoney(amount)),
		Data: map[string]string{
			"type":         TypePaymentConfirmed,
			"group_id":     groupID,
			"payment_id":   paymentID,
			"amount":       strconv.FormatInt(amount, 10),
			"click_action": DefaultFlutterClickAction,
		},
	}
}

// NewPaymentRejectedMessage tạo thông báo khi chủ nợ từ chối thanh toán
func NewPaymentRejectedMessage(rejecterName, reason string, groupID, paymentID string) PushMessage {
	body := fmt.Sprintf("%s đã từ chối xác nhận thanh toán của bạn.", rejecterName)
	if reason != "" {
		body = fmt.Sprintf("%s đã từ chối xác nhận thanh toán: %s", rejecterName, reason)
	}

	return PushMessage{
		Title: "Thanh toán bị từ chối ❌",
		Body:  body,
		Data: map[string]string{
			"type":         TypePaymentRejected,
			"group_id":     groupID,
			"payment_id":   paymentID,
			"click_action": DefaultFlutterClickAction,
		},
	}
}

// NewGroupInvitationMessage tạo thông báo khi được mời vào nhóm mới
func NewGroupInvitationMessage(inviterName, groupName, groupID, inviteCode string) PushMessage {
	return PushMessage{
		Title: "Lời mời tham gia nhóm 👥",
		Body:  fmt.Sprintf("%s đã mời bạn tham gia nhóm '%s'.", inviterName, groupName),
		Data: map[string]string{
			"type":         TypeGroupInvitation,
			"group_id":     groupID,
			"invite_code":  inviteCode,
			"click_action": DefaultFlutterClickAction,
		},
	}
}

// NewSystemAnnouncementMessage tạo thông báo chung từ hệ thống
func NewSystemAnnouncementMessage(title, body string, extraData map[string]string) PushMessage {
	data := make(map[string]string)
	data["type"] = TypeSystemAnnouncement
	data["click_action"] = DefaultFlutterClickAction
	for k, v := range extraData {
		data[k] = v
	}

	return PushMessage{
		Title: title,
		Body:  body,
		Data:  data,
	}
}

// formatMoney định dạng số tiền có dấu phân cách hàng nghìn (VD: 1500000 -> 1.500.000)
func formatMoney(n int64) string {
	in := strconv.FormatInt(n, 10)
	out := make([]byte, len(in)+(len(in)-1)/3)
	for i, j, k := len(in)-1, len(out)-1, 0; i >= 0; i, j = i-1, j-1 {
		out[j] = in[i]
		k++
		if k%3 == 0 && i > 0 {
			j--
			out[j] = '.'
		}
	}
	return string(out)
}
