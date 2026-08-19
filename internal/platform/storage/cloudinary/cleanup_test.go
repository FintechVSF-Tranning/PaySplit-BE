package cloudinary_test

import (
	"context"
	"errors"
	"testing"

	"paysplit-backend/internal/platform/storage/cloudinary"
)

type fakeDeleter struct {
	calledWith []string
	err        error
}

func (f *fakeDeleter) Delete(ctx context.Context, publicID string) error {
	f.calledWith = append(f.calledWith, publicID)
	return f.err
}

func TestCleanupStorage_BillKey_RoutesToBillDeleter(t *testing.T) {
	// covers: AC-13 (a bill receipt object key must be destroyed as a private asset, not routed to the avatar deleter)
	avatar := &fakeDeleter{}
	bill := &fakeDeleter{}
	storage := cloudinary.NewCleanupStorage(avatar, bill)

	if err := storage.Delete(context.Background(), "bills/op-1/0"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if len(bill.calledWith) != 1 || bill.calledWith[0] != "bills/op-1/0" {
		t.Errorf("expected the bill deleter to receive the key, got avatar=%v bill=%v", avatar.calledWith, bill.calledWith)
	}
	if len(avatar.calledWith) != 0 {
		t.Errorf("expected the avatar deleter not to be called for a bill key, got %v", avatar.calledWith)
	}
}

func TestCleanupStorage_AvatarKey_RoutesToAvatarDeleter(t *testing.T) {
	// covers: AC-13 (a non bill object key keeps going through the avatar deleter, preserving existing behavior)
	avatar := &fakeDeleter{}
	bill := &fakeDeleter{}
	storage := cloudinary.NewCleanupStorage(avatar, bill)

	if err := storage.Delete(context.Background(), "paysplit/avatars/user-1/abc"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if len(avatar.calledWith) != 1 || avatar.calledWith[0] != "paysplit/avatars/user-1/abc" {
		t.Errorf("expected the avatar deleter to receive the key, got avatar=%v bill=%v", avatar.calledWith, bill.calledWith)
	}
	if len(bill.calledWith) != 0 {
		t.Errorf("expected the bill deleter not to be called for an avatar key, got %v", bill.calledWith)
	}
}

func TestCleanupStorage_PropagatesDeleterError(t *testing.T) {
	// covers: AC-13, AC-14 (a real destroy failure must propagate so the cleanup job retries, not report success)
	wantErr := errors.New("destroy failed")
	storage := cloudinary.NewCleanupStorage(&fakeDeleter{}, &fakeDeleter{err: wantErr})

	if err := storage.Delete(context.Background(), "bills/op-1/0"); !errors.Is(err, wantErr) {
		t.Errorf("Delete() error = %v, want %v", err, wantErr)
	}
}
