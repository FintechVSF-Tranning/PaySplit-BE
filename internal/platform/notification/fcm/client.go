package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"

	"paysplit-backend/internal/modules/notification/domain"
)

const (
	// Topic mặc định phát thông báo cho toàn bộ người dùng
	topicAllUsers = "all_users"
)

var (
	// ErrInvalidToken báo hiệu FCM Registration Token không còn tồn tại (user đã gỡ app),
	// đã hết hạn hoặc không đúng định dạng. Khi gặp lỗi này, Backend nên xóa fcm_token khỏi database.
	ErrInvalidToken = errors.New("fcm: invalid or unregistered registration token")

	// ErrInvalidMessage báo hiệu FCM từ chối nội dung message (payload/data key/APNs config sai
	// định dạng), không liên quan đến token. Backend KHÔNG được xóa fcm_token khi gặp lỗi này,
	// vì token vẫn hợp lệ, chỉ có payload đang gửi là sai.
	ErrInvalidMessage = errors.New("fcm: invalid message payload")
)

type PushMessage = domain.PushMessage

type Notifier struct {
	client  *messaging.Client
	timeout time.Duration
}

func New(ctx context.Context, credentialsFile, credentialsJSON string, timeout time.Duration) (*Notifier, error) {
	credentialsFile = strings.TrimSpace(credentialsFile)
	credentialsJSON = strings.TrimSpace(credentialsJSON)
	if strings.HasPrefix(credentialsJSON, "'") && strings.HasSuffix(credentialsJSON, "'") {
		credentialsJSON = strings.Trim(credentialsJSON, "'")
	}
	if strings.HasPrefix(credentialsJSON, "\"") && strings.HasSuffix(credentialsJSON, "\"") {
		credentialsJSON = strings.Trim(credentialsJSON, "\"")
	}

	if credentialsFile == "" && credentialsJSON == "" {
		return nil, nil // Disabled if no credentials configured
	}

	var fbConfig *firebase.Config
	var opt option.ClientOption
	if credentialsJSON != "" {
		var parsed struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal([]byte(credentialsJSON), &parsed); err == nil && parsed.ProjectID != "" {
			fbConfig = &firebase.Config{ProjectID: parsed.ProjectID}
		}
		opt = option.WithCredentialsJSON([]byte(credentialsJSON))
	} else {
		opt = option.WithCredentialsFile(credentialsFile)
	}

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	app, err := firebase.NewApp(initCtx, fbConfig, opt)
	if err != nil {
		return nil, fmt.Errorf("create firebase app: %w", err)
	}

	client, err := app.Messaging(initCtx)
	if err != nil {
		return nil, fmt.Errorf("create fcm messaging client: %w", err)
	}

	return &Notifier{
		client:  client,
		timeout: timeout,
	}, nil
}

// SendToDevice gửi thông báo đẩy tới 1 thiết bị cụ thể thông qua FCM Token.
// Nếu token không còn hợp lệ (user gỡ app), hàm trả về lỗi chứa ErrInvalidToken để caller dọn dẹp database.
func (n *Notifier) SendToDevice(ctx context.Context, fcmToken string, msg PushMessage) error {
	if n == nil || n.client == nil || fcmToken == "" {
		return nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

	fcmMsg := n.buildFCMMessage(msg)
	fcmMsg.Token = fcmToken

	_, err := n.client.Send(sendCtx, fcmMsg)
	if err != nil {
		if messaging.IsRegistrationTokenNotRegistered(err) {
			return fmt.Errorf("%w: %v", ErrInvalidToken, err)
		}
		if messaging.IsInvalidArgument(err) {
			return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
		}
		return fmt.Errorf("send FCM message to device: %w", err)
	}
	return nil
}

// SendToAllUsers gửi thông báo hệ thống tới toàn bộ người dùng đã cài ứng dụng
func (n *Notifier) SendToAllUsers(ctx context.Context, msg PushMessage) error {
	if n == nil || n.client == nil {
		return nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

	fcmMsg := n.buildFCMMessage(msg)
	fcmMsg.Topic = topicAllUsers

	_, err := n.client.Send(sendCtx, fcmMsg)
	if err != nil {
		return fmt.Errorf("send FCM message to all users: %w", err)
	}
	return nil
}

// IsInvalidTokenError kiểm tra xem lỗi trả về có phải do Token đã chết hoặc không hợp lệ hay không.
// Chỉ token thực sự không còn đăng ký (NOT_REGISTERED) mới được coi là chết; các lỗi payload
// (INVALID_ARGUMENT) không nằm trong nhóm này, xem IsInvalidMessageError.
func IsInvalidTokenError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrInvalidToken) || messaging.IsRegistrationTokenNotRegistered(err)
}

// IsInvalidToken triển khai phương thức cho interface PushNotifier ở tầng usecase.
func (n *Notifier) IsInvalidToken(err error) bool {
	return IsInvalidTokenError(err)
}

// IsInvalidMessageError kiểm tra xem lỗi trả về có phải do nội dung message gửi lên FCM không
// hợp lệ hay không (payload/data key/APNs config sai định dạng). Lỗi này không kéo theo việc
// xóa fcm_token vì bản thân token vẫn có thể còn hợp lệ.
func IsInvalidMessageError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrInvalidMessage) || messaging.IsInvalidArgument(err)
}

func (n *Notifier) buildFCMMessage(msg PushMessage) *messaging.Message {
	return &messaging.Message{
		Notification: &messaging.Notification{
			Title: msg.Title,
			Body:  msg.Body,
		},
		Data: msg.Data,
		Android: &messaging.AndroidConfig{
			Priority: "high",
			Notification: &messaging.AndroidNotification{
				Sound: "default",
			},
		},
		APNS: &messaging.APNSConfig{
			Payload: &messaging.APNSPayload{
				Aps: &messaging.Aps{
					Sound: "default",
				},
			},
		},
	}
}
