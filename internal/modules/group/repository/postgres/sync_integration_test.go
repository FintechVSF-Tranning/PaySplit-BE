package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/group/domain"
	"paysplit-backend/internal/modules/group/repository"
)

// mustInviteCode tạo một invite dùng được ngay cho nhóm.
func mustInviteCode(t *testing.T, ctx context.Context, repo repository.Repository, groupID, captainID string) string {
	t.Helper()
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{
		GroupID:      groupID,
		CallerUserID: captainID,
		Code:         code,
		ExpiresIn:    24 * time.Hour,
		MaxUses:      nil,
	}); err != nil {
		t.Fatal(err)
	}
	return code
}

func readRosterVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID string) int64 {
	t.Helper()
	var version int64
	if err := pool.QueryRow(ctx, `SELECT roster_version FROM groups WHERE id=$1`, groupID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

// Mỗi mutation phải ghi đúng một dòng nhật ký, và roster_version phải khớp
// version của dòng cuối cùng: nhật ký và con trỏ không được phép trôi khỏi nhau.
func TestEmitGroupEvent_WritesLogAtomicallyWithTheMutation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	joinerID := createUser(t, ctx, pool, cleanup, "Joiner")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Sync Trip", captainID)

	// Nhóm mới chưa có mutation nào nên chưa có version.
	if v := readRosterVersion(t, ctx, pool, group.ID); v != 0 {
		t.Fatalf("roster_version của nhóm mới = %d, want 0", v)
	}

	code := mustInviteCode(t, ctx, repo, group.ID, captainID)
	join, err := repo.RedeemInvite(ctx, code, joinerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RenameGroup(ctx, group.ID, captainID, "Sync Trip v2"); err != nil {
		t.Fatal(err)
	}
	if err = repo.LeaveOrRemoveMember(ctx, group.ID, join.MembershipID, joinerID); err != nil {
		t.Fatal(err)
	}

	events, err := repo.ListEventsSince(ctx, group.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{domain.EventMemberJoined, domain.EventGroupRenamed, domain.EventMemberLeft}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %d (%+v), want %d", len(events), events, len(wantTypes))
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("events[%d].Type = %q, want %q", i, events[i].Type, want)
		}
		if events[i].Version != int64(i+1) {
			t.Fatalf("events[%d].Version = %d, want %d — dãy version phải liền mạch", i, events[i].Version, i+1)
		}
	}

	// Sự kiện thêm thành viên phải mang đủ dữ liệu để client dựng được một dòng
	// trong danh sách mà không phải gọi thêm API.
	var joinedPayload struct {
		Member struct {
			MembershipID string `json:"membership_id"`
			DisplayName  string `json:"display_name"`
			Role         string `json:"role"`
		} `json:"member"`
		ActiveMemberCount int `json:"active_member_count"`
	}
	if err = json.Unmarshal(events[0].Payload, &joinedPayload); err != nil {
		t.Fatal(err)
	}
	if joinedPayload.Member.MembershipID != join.MembershipID || joinedPayload.Member.DisplayName != "Joiner" {
		t.Fatalf("member payload = %+v", joinedPayload.Member)
	}
	if joinedPayload.ActiveMemberCount != 2 {
		t.Fatalf("active_member_count = %d, want 2", joinedPayload.ActiveMemberCount)
	}

	cursor, err := repo.GetSyncCursor(ctx, group.ID, captainID)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Current != 3 || cursor.Oldest != 1 {
		t.Fatalf("cursor = %+v, want {Current:3 Oldest:1}", cursor)
	}
	if v := readRosterVersion(t, ctx, pool, group.ID); v != cursor.Current {
		t.Fatalf("roster_version = %d nhưng cursor.Current = %d", v, cursor.Current)
	}
}

// Nhiều thành viên vào cùng lúc là chính kịch bản đã gây ra lỗi: dãy version
// phải liền mạch, không trùng, không sót — kể cả khi các transaction chạy song
// song. Đây là bất biến mà sequence toàn cục không đảm bảo được.
func TestEmitGroupEvent_ConcurrentJoinsProduceAGaplessSequence(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Concurrent Trip", captainID)
	code := mustInviteCode(t, ctx, repo, group.ID, captainID)

	const joiners = 10
	userIDs := make([]string, joiners)
	for i := range userIDs {
		userIDs[i] = createUser(t, ctx, pool, cleanup, fmt.Sprintf("Joiner%d", i))
	}

	var wg sync.WaitGroup
	var failures atomic.Int32
	for _, userID := range userIDs {
		wg.Add(1)
		go func(uid string) {
			defer wg.Done()
			if _, err := repo.RedeemInvite(ctx, code, uid); err != nil {
				failures.Add(1)
				t.Errorf("join %s: %v", uid, err)
			}
		}(userID)
	}
	wg.Wait()
	if failures.Load() > 0 {
		t.FailNow()
	}

	events, err := repo.ListEventsSince(ctx, group.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != joiners {
		t.Fatalf("events = %d, want %d", len(events), joiners)
	}
	for i, event := range events {
		if event.Version != int64(i+1) {
			t.Fatalf("events[%d].Version = %d, want %d — dãy version bị hở hoặc đảo thứ tự", i, event.Version, i+1)
		}
		if event.Type != domain.EventMemberJoined {
			t.Fatalf("events[%d].Type = %q", i, event.Type)
		}
	}
	if v := readRosterVersion(t, ctx, pool, group.ID); v != int64(joiners) {
		t.Fatalf("roster_version = %d, want %d", v, joiners)
	}
}

// Mutation thất bại không được để lại sự kiện mồ côi: nhật ký nằm cùng
// transaction với thay đổi dữ liệu.
func TestEmitGroupEvent_RolledBackMutationLeavesNoEvent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Rollback Trip", captainID)

	// Captain không được rời nhóm: mutation dừng lại trước khi ghi bất cứ thứ gì.
	membership, err := repo.GetGroupDetail(ctx, group.ID, captainID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.LeaveOrRemoveMember(ctx, group.ID, membership.Members[0].MembershipID, captainID); err == nil {
		t.Fatal("Captain rời nhóm phải bị từ chối")
	}

	events, err := repo.ListEventsSince(ctx, group.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d (%+v), want 0", len(events), events)
	}
	if v := readRosterVersion(t, ctx, pool, group.ID); v != 0 {
		t.Fatalf("roster_version = %d, want 0 — transaction bị rollback vẫn tiêu tốn version", v)
	}
}

// Snapshot phải nhất quán: version đi kèm danh sách thành viên của đúng cùng
// thời điểm, nếu không client sẽ bỏ qua sự kiện kế tiếp và giữ state sai.
func TestGetGroupDetail_VersionMatchesTheMemberListItReturns(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	joinerID := createUser(t, ctx, pool, cleanup, "Joiner")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Snapshot Trip", captainID)

	before, err := repo.GetGroupDetail(ctx, group.ID, captainID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Version != 0 || len(before.Members) != 1 {
		t.Fatalf("before = version %d với %d thành viên", before.Version, len(before.Members))
	}

	code := mustInviteCode(t, ctx, repo, group.ID, captainID)
	if _, err = repo.RedeemInvite(ctx, code, joinerID); err != nil {
		t.Fatal(err)
	}

	after, err := repo.GetGroupDetail(ctx, group.ID, captainID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != 1 || len(after.Members) != 2 {
		t.Fatalf("after = version %d với %d thành viên, want version 1 với 2 thành viên", after.Version, len(after.Members))
	}
}
