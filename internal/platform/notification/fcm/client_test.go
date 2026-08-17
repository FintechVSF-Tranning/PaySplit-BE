package fcm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestNew_DisabledWhenNoConfig(t *testing.T) {
	ctx := context.Background()
	notifier, err := New(ctx, "", "", 5*time.Second)
	if err != nil {
		t.Fatalf("expected no error when credentials are empty, got: %v", err)
	}
	if notifier != nil {
		t.Fatalf("expected nil notifier when no credentials provided")
	}
}

func TestNilNotifier_SendToDeviceSafe(t *testing.T) {
	ctx := context.Background()
	var notifier *Notifier

	if err := notifier.SendToDevice(ctx, "sample-token", PushMessage{Title: "Title", Body: "Body"}); err != nil {
		t.Fatalf("expected nil notifier SendToDevice to return nil, got: %v", err)
	}
}

func TestSendToDevice_EmptyTokenSafe(t *testing.T) {
	ctx := context.Background()
	notifier := &Notifier{timeout: 5 * time.Second}

	if err := notifier.SendToDevice(ctx, "", PushMessage{Title: "Title", Body: "Body"}); err != nil {
		t.Fatalf("expected empty token SendToDevice to return nil, got: %v", err)
	}
}

func TestSendToAllUsers_NilNotifierSafe(t *testing.T) {
	ctx := context.Background()
	var notifier *Notifier

	if err := notifier.SendToAllUsers(ctx, PushMessage{Title: "Title", Body: "Body"}); err != nil {
		t.Fatalf("expected nil notifier SendToAllUsers to return nil, got: %v", err)
	}
}

func TestIsInvalidTokenError(t *testing.T) {
	if IsInvalidTokenError(nil) {
		t.Errorf("expected false for nil error")
	}

	wrappedErr := fmt.Errorf("outer: %w", ErrInvalidToken)
	if !IsInvalidTokenError(wrappedErr) {
		t.Errorf("expected true for wrapped ErrInvalidToken")
	}

	otherErr := errors.New("network timeout")
	if IsInvalidTokenError(otherErr) {
		t.Errorf("expected false for unrelated error")
	}
}
