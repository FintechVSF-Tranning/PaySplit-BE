package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Một lượt xác nhận thanh toán gọi notifyAll 3 + N lần trong cùng transaction.
// Không có bộ nhớ này thì mỗi lần lại một truy vấn group_members y hệt nhau.
func TestAudienceCacheServesRepeatedReadsWithinOneTransaction(t *testing.T) {
	ctx := WithAudienceCache(context.Background())
	groupID := uuid.Must(uuid.NewV7())

	if _, ok := audienceFromContext(ctx, groupID); ok {
		t.Fatal("bộ nhớ rỗng không được báo là đã có dữ liệu")
	}

	want := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	rememberAudience(ctx, groupID, want)

	got, ok := audienceFromContext(ctx, groupID)
	if !ok || len(got) != len(want) {
		t.Fatalf("audienceFromContext() = %v, %v; want %v, true", got, ok, want)
	}

	// Nhóm khác không dùng chung kết quả.
	if _, ok = audienceFromContext(ctx, uuid.Must(uuid.NewV7())); ok {
		t.Error("nhóm khác không được dùng lại audience của nhóm này")
	}
}

func TestAudienceCacheIsScopedToOneTransaction(t *testing.T) {
	groupID := uuid.Must(uuid.NewV7())
	first := WithAudienceCache(context.Background())
	rememberAudience(first, groupID, []uuid.UUID{uuid.Must(uuid.NewV7())})

	// Transaction sau phải đọc lại: danh sách thành viên có thể đã đổi.
	second := WithAudienceCache(context.Background())
	if _, ok := audienceFromContext(second, groupID); ok {
		t.Error("bộ nhớ rò rỉ sang transaction khác")
	}

	// Gắn hai lần trên cùng một ctx không được xóa những gì đã nhớ.
	again := WithAudienceCache(first)
	if _, ok := audienceFromContext(again, groupID); !ok {
		t.Error("gắn lại trên cùng ctx làm mất bộ nhớ đang có")
	}
}
