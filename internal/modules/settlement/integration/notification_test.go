package integration

import (
	"context"
	"errors"
	"testing"

	notificationdomain "paysplit-backend/internal/modules/notification/domain"
	notificationrepo "paysplit-backend/internal/modules/notification/repository"
)

type notificationRepositoryStub struct {
	notificationrepo.Repository
	created *notificationdomain.Notification
	err     error
}

func (r *notificationRepositoryStub) CreateNotificationTx(_ context.Context, _ notificationrepo.Executor, notification *notificationdomain.Notification) error {
	r.created = notification
	if notification.ID == "" {
		notification.ID = "notification"
	}
	return r.err
}

type enqueuerStub struct {
	id  string
	err error
}

func (e *enqueuerStub) EnqueueNotificationTx(_ context.Context, _ notificationrepo.Executor, id string) error {
	e.id = id
	return e.err
}

func TestNotifier_AC6ThroughAC10CreatesAndEnqueuesTypedNotification(t *testing.T) {
	kinds := []string{"payment_submitted", "payment_confirmed", "payment_rejected", "debt_reminded", "payment_stalled_confirmation"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			repo := &notificationRepositoryStub{}
			queue := &enqueuerStub{}
			notifier := NewNotifier(repo, queue)
			if err := notifier.NotifyTx(context.Background(), struct{}{}, "user", kind, map[string]string{"type": kind}); err != nil {
				t.Fatal(err)
			}
			if repo.created == nil || repo.created.UserID != "user" || repo.created.Type != kind || repo.created.Title == "" || repo.created.Body == "" {
				t.Fatalf("unexpected notification: %+v", repo.created)
			}
			if queue.id != "notification" {
				t.Fatalf("queued id=%q", queue.id)
			}
		})
	}
}

func TestNotifier_AC7RollsBackWhenNotificationOrQueueFails(t *testing.T) {
	want := errors.New("failure")
	for _, tc := range []struct {
		name              string
		repoErr, queueErr error
	}{{"repository", want, nil}, {"queue", nil, want}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &notificationRepositoryStub{err: tc.repoErr}
			queue := &enqueuerStub{err: tc.queueErr}
			if err := NewNotifier(repo, queue).NotifyTx(context.Background(), struct{}{}, "user", "payment_confirmed", nil); err == nil {
				t.Fatal("expected transaction callback error")
			}
			if tc.repoErr != nil && queue.id != "" {
				t.Fatal("queue ran after repository failure")
			}
		})
	}
}
