package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/group/domain"
	"paysplit-backend/internal/modules/group/repository"
)

// --- test infrastructure -----------------------------------------------

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testCleanup deletes groups (which cascades to their members, invites,
// activities, bills, and debts) before deleting the users that owned them,
// matching the FK direction: groups.created_by -> users, but group scoped
// tables cascade from groups.
type testCleanup struct {
	pool   *pgxpool.Pool
	groups []string
	users  []string
}

func newCleanup(t *testing.T, pool *pgxpool.Pool) *testCleanup {
	c := &testCleanup{pool: pool}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, g := range c.groups {
			_, _ = pool.Exec(ctx, `DELETE FROM groups WHERE id=$1`, g)
		}
		for _, u := range c.users {
			_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, u)
		}
	})
	return c
}

func (c *testCleanup) trackGroup(id string) { c.groups = append(c.groups, id) }
func (c *testCleanup) trackUser(id string)  { c.users = append(c.users, id) }

var testUserSeq atomic.Int64

func createUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cleanup *testCleanup, displayName string) string {
	t.Helper()
	seq := testUserSeq.Add(1)
	email := fmt.Sprintf("group.repo.test.%d.%d@example.invalid", time.Now().UnixNano(), seq)
	phone := fmt.Sprintf("+849%08d", seq%100000000)
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, display_name, phone_number, role, status, email_verified_at) VALUES ($1,'x',$2,$3,'user','active',now()) RETURNING id`, email, displayName, phone).Scan(&id)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	cleanup.trackUser(id)
	return id
}

func mustCreateGroup(t *testing.T, ctx context.Context, repo repository.Repository, cleanup *testCleanup, name, createdBy string) (*domain.Group, *domain.Membership) {
	t.Helper()
	group, membership, err := repo.CreateGroup(ctx, repository.CreateGroupParams{Name: name, Currency: "VND", CreatedBy: createdBy})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	cleanup.trackGroup(group.ID)
	return group, membership
}

func activityMetadata(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID, actionType string) map[string]any {
	t.Helper()
	var raw []byte
	err := pool.QueryRow(ctx, `SELECT metadata FROM group_activities WHERE group_id=$1 AND action_type=$2 ORDER BY created_at DESC LIMIT 1`, groupID, actionType).Scan(&raw)
	if err != nil {
		t.Fatalf("read %s activity metadata: %v", actionType, err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal activity metadata: %v", err)
	}
	return meta
}

func countActivities(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID, actionType string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_activities WHERE group_id=$1 AND action_type=$2`, groupID, actionType).Scan(&n); err != nil {
		t.Fatalf("count %s activities: %v", actionType, err)
	}
	return n
}

// --- CreateGroup (AC-1) --------------------------------------------------

func TestCreateGroup_CreatesGroupCaptainMembershipAndActivityAtomically(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	userID := createUser(t, ctx, pool, cleanup, "Captain")
	group, membership := mustCreateGroup(t, ctx, repo, cleanup, "Trip", userID)

	if group.Name != "Trip" || group.Currency != "VND" {
		t.Fatalf("unexpected group: %+v", group)
	}
	if membership.Role != domain.RoleCaptain || membership.Status != domain.MembershipActive {
		t.Fatalf("unexpected initial membership: %+v", membership)
	}
	if membership.UserID != userID {
		t.Fatalf("membership.UserID = %q, want %q", membership.UserID, userID)
	}
	if n := countActivities(t, ctx, pool, group.ID, "group_created"); n != 1 {
		t.Fatalf("group_created activities = %d, want exactly 1", n)
	}
	meta := activityMetadata(t, ctx, pool, group.ID, "group_created")
	if meta["group_id"] != group.ID {
		t.Fatalf("group_created metadata = %+v, want group_id %q", meta, group.ID)
	}
}

func TestCreateGroup_RejectsAMalformedCreatedByID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := New(pool)

	if _, _, err := repo.CreateGroup(ctx, repository.CreateGroupParams{Name: "Trip", Currency: "VND", CreatedBy: "not-a-uuid"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// --- ListGroups (AC-2) ----------------------------------------------------

func TestListGroups_CursorPaginationOrdersNewestFirstWithNoDuplicates(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	userID := createUser(t, ctx, pool, cleanup, "Lister")
	var created []string
	for i := 0; i < 3; i++ {
		g, _ := mustCreateGroup(t, ctx, repo, cleanup, fmt.Sprintf("Group %d", i), userID)
		created = append(created, g.ID)
		time.Sleep(2 * time.Millisecond) // keep created_at strictly increasing
	}

	seen := map[string]bool{}
	var cursor *string
	for page := 0; page < 10; page++ {
		items, next, err := repo.ListGroups(ctx, repository.ListGroupsParams{UserID: userID, Cursor: cursor, Limit: 1})
		if err != nil {
			t.Fatalf("list groups page %d: %v", page, err)
		}
		if len(items) != 1 {
			t.Fatalf("page %d: got %d items, want 1", page, len(items))
		}
		id := items[0].Group.ID
		if seen[id] {
			t.Fatalf("group %s returned twice across pages", id)
		}
		seen[id] = true
		if next == nil {
			break
		}
		cursor = next
	}
	if len(seen) != len(created) {
		t.Fatalf("saw %d distinct groups across pages, want %d", len(seen), len(created))
	}
	// Newest first: the last group created must be the first page's item.
	first, _, err := repo.ListGroups(ctx, repository.ListGroupsParams{UserID: userID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Group.ID != created[len(created)-1] {
		t.Fatalf("first page = %s, want the most recently created group %s", first[0].Group.ID, created[len(created)-1])
	}
}

func TestListGroups_RejectsAnUndecodableCursor(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	userID := createUser(t, ctx, pool, cleanup, "CursorTester")
	badCursor := "not-a-real-cursor"
	if _, _, err := repo.ListGroups(ctx, repository.ListGroupsParams{UserID: userID, Cursor: &badCursor}); !errors.Is(err, domain.ErrInvalidCursor) {
		t.Fatalf("error = %v, want ErrInvalidCursor", err)
	}
}

// --- GetGroupDetail (AC-2) -------------------------------------------------

func TestGetGroupDetail_MissingGroupAndNonMemberReturnIdenticalNotFound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	outsiderID := createUser(t, ctx, pool, cleanup, "Outsider")
	group, membership := mustCreateGroup(t, ctx, repo, cleanup, "Private Trip", captainID)

	detail, err := repo.GetGroupDetail(ctx, group.ID, captainID)
	if err != nil {
		t.Fatalf("active member detail: unexpected error: %v", err)
	}
	if detail.CallerRole != domain.RoleCaptain || len(detail.Members) != 1 {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	if detail.Members[0].MembershipID != membership.ID {
		t.Fatalf("member id = %q, want %q", detail.Members[0].MembershipID, membership.ID)
	}

	_, nonMemberErr := repo.GetGroupDetail(ctx, group.ID, outsiderID)
	if !errors.Is(nonMemberErr, domain.ErrGroupNotFound) {
		t.Fatalf("nonmember error = %v, want ErrGroupNotFound", nonMemberErr)
	}
	_, missingGroupErr := repo.GetGroupDetail(ctx, "018f0000-0000-7000-8000-000000000000", outsiderID)
	if !errors.Is(missingGroupErr, domain.ErrGroupNotFound) {
		t.Fatalf("missing group error = %v, want ErrGroupNotFound", missingGroupErr)
	}
	if nonMemberErr.Error() != missingGroupErr.Error() {
		t.Fatalf("nonmember and missing group errors differ (%v vs %v), group existence would be leaked", nonMemberErr, missingGroupErr)
	}
}

// --- Invite lifecycle (AC-3, AC-8) ----------------------------------------

func TestCreateOrReuseInvite_ReusesRegeneratesAndRequiresCaptain(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberID := createUser(t, ctx, pool, cleanup, "Member")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Invite Trip", captainID)

	code1, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code1, ExpiresIn: 24 * time.Hour})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if !first.Created {
		t.Fatal("first CreateOrReuseInvite: Created = false, want true")
	}

	code2, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	reused, err := repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code2, ExpiresIn: 24 * time.Hour})
	if err != nil {
		t.Fatalf("reuse invite: %v", err)
	}
	if reused.Created {
		t.Fatal("reuse: Created = true, want false (should reuse the available invite)")
	}
	if reused.Invite.ID != first.Invite.ID || reused.Invite.Code != first.Invite.Code {
		t.Fatalf("reuse returned a different invite: got %+v, want the first invite %+v", reused.Invite, first.Invite)
	}

	code3, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	regenerated, err := repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code3, ExpiresIn: 48 * time.Hour, Regenerate: true})
	if err != nil {
		t.Fatalf("regenerate invite: %v", err)
	}
	if !regenerated.Created || regenerated.Invite.ID == first.Invite.ID {
		t.Fatalf("regenerate did not produce a new invite: %+v", regenerated)
	}
	if n := countActivities(t, ctx, pool, group.ID, "invite_revoked"); n != 1 {
		t.Fatalf("invite_revoked activities = %d, want exactly 1 (the reused invite was revoked once)", n)
	}
	meta := activityMetadata(t, ctx, pool, group.ID, "invite_created")
	if meta["invite_id"] == nil || meta["invite_id"] == "" {
		t.Fatalf("invite_created metadata missing invite_id: %+v", meta)
	}
	if _, hasCode := meta["code"]; hasCode {
		t.Fatalf("invite_created metadata leaks the invite code: %+v", meta)
	}

	code4, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: memberID, Code: code4, ExpiresIn: 24 * time.Hour}); !errors.Is(err, domain.ErrCaptainRequired) {
		t.Fatalf("non-Captain caller: error = %v, want ErrCaptainRequired", err)
	}
}

func TestRevokeInvite_IsIdempotentAndRejectsAnUnknownInvite(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Revoke Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	invite, err := repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}

	if err = repo.RevokeInvite(ctx, group.ID, invite.Invite.ID, captainID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err = repo.RevokeInvite(ctx, group.ID, invite.Invite.ID, captainID); err != nil {
		t.Fatalf("second revoke (idempotent retry): %v", err)
	}
	if err = repo.RevokeInvite(ctx, group.ID, "018f0000-0000-7000-8000-000000000000", captainID); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("unknown invite: error = %v, want ErrInviteNotFound", err)
	}
}

func TestPreviewInvite_UnknownAndUnavailableCodes(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Preview Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	invite, err := repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := repo.PreviewInvite(ctx, code)
	if err != nil {
		t.Fatalf("valid code: %v", err)
	}
	if preview.GroupName != "Preview Trip" || preview.ActiveMemberCount != 1 || preview.CaptainDisplayName != "Captain" {
		t.Fatalf("unexpected preview: %+v", preview)
	}

	if _, err = repo.PreviewInvite(ctx, "totally-unknown-code"); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("unknown code: error = %v, want ErrInviteNotFound", err)
	}

	if err = repo.RevokeInvite(ctx, group.ID, invite.Invite.ID, captainID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.PreviewInvite(ctx, code); !errors.Is(err, domain.ErrInviteUnavailable) {
		t.Fatalf("revoked code: error = %v, want ErrInviteUnavailable", err)
	}
}

// --- Redeem invite: join, reactivate, capacity (AC-5, AC-8) ---------------

func TestRedeemInvite_JoinsThenIsIdempotentThenReactivates(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	joinerID := createUser(t, ctx, pool, cleanup, "Joiner")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Join Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}

	joined, err := repo.RedeemInvite(ctx, code, joinerID)
	if err != nil {
		t.Fatalf("first join: %v", err)
	}
	if joined.Result != domain.JoinResultJoined {
		t.Fatalf("Result = %q, want %q", joined.Result, domain.JoinResultJoined)
	}
	if n := countActivities(t, ctx, pool, group.ID, "member_joined"); n != 1 {
		t.Fatalf("member_joined activities = %d, want exactly 1", n)
	}

	again, err := repo.RedeemInvite(ctx, code, joinerID)
	if err != nil {
		t.Fatalf("repeat join: %v", err)
	}
	if again.Result != domain.JoinResultAlreadyActive || again.MembershipID != joined.MembershipID {
		t.Fatalf("repeat join = %+v, want already_active with the same membership id %q", again, joined.MembershipID)
	}
	if n := countActivities(t, ctx, pool, group.ID, "member_joined"); n != 1 {
		t.Fatalf("member_joined activities after repeat join = %d, want still 1 (idempotent join writes no activity)", n)
	}
	var useCount int
	if err = pool.QueryRow(ctx, `SELECT use_count FROM group_invites WHERE code=$1`, code).Scan(&useCount); err != nil {
		t.Fatal(err)
	}
	if useCount != 1 {
		t.Fatalf("invite use_count = %d, want 1 (idempotent join must not increment it again)", useCount)
	}

	if _, err = pool.Exec(ctx, `UPDATE group_members SET status='inactive', left_at=now() WHERE id=$1`, joined.MembershipID); err != nil {
		t.Fatal(err)
	}
	reactivated, err := repo.RedeemInvite(ctx, code, joinerID)
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if reactivated.Result != domain.JoinResultReactivated || reactivated.MembershipID != joined.MembershipID {
		t.Fatalf("reactivate = %+v, want reactivated with the original membership id %q", reactivated, joined.MembershipID)
	}
	if n := countActivities(t, ctx, pool, group.ID, "member_reactivated"); n != 1 {
		t.Fatalf("member_reactivated activities = %d, want exactly 1", n)
	}
}

func TestRedeemInvite_UnknownAndUnavailableCodes(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	joinerID := createUser(t, ctx, pool, cleanup, "Joiner")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Bad Code Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	invite, err := repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = repo.RedeemInvite(ctx, "unknown-code-xyz", joinerID); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("unknown code: error = %v, want ErrInviteNotFound", err)
	}

	if err = repo.RevokeInvite(ctx, group.ID, invite.Invite.ID, captainID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RedeemInvite(ctx, code, joinerID); !errors.Is(err, domain.ErrInviteUnavailable) {
		t.Fatalf("revoked code: error = %v, want ErrInviteUnavailable", err)
	}
}

func TestRedeemInvite_RejectsAtTheFiftyMemberCapacityLimit(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Full Trip", captainID)

	// Seed 48 more active members directly (49 total with the Captain),
	// leaving exactly one open slot to reach the 50 member cap.
	for i := 0; i < 48; i++ {
		uid := createUser(t, ctx, pool, cleanup, fmt.Sprintf("Seed%d", i))
		if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, status) VALUES ($1,$2,'member','active')`, group.ID, uid); err != nil {
			t.Fatal(err)
		}
	}

	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}

	lastSlot := createUser(t, ctx, pool, cleanup, "LastSlot")
	if _, err = repo.RedeemInvite(ctx, code, lastSlot); err != nil {
		t.Fatalf("50th member should join successfully: %v", err)
	}

	overflow := createUser(t, ctx, pool, cleanup, "Overflow")
	if _, err = repo.RedeemInvite(ctx, code, overflow); !errors.Is(err, domain.ErrGroupMemberLimitReached) {
		t.Fatalf("51st member: error = %v, want ErrGroupMemberLimitReached", err)
	}
	var useCount int
	if err = pool.QueryRow(ctx, `SELECT use_count FROM group_invites WHERE code=$1`, code).Scan(&useCount); err != nil {
		t.Fatal(err)
	}
	if useCount != 1 {
		t.Fatalf("invite use_count after the rejected join = %d, want 1 (the rejected attempt must not increment it)", useCount)
	}
}

func TestRedeemInvite_ConcurrentRedemptionsNeverExceedMaxUses(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Race Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	maxUses := 1
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour, MaxUses: &maxUses}); err != nil {
		t.Fatal(err)
	}

	const racers = 10
	userIDs := make([]string, racers)
	for i := range userIDs {
		userIDs[i] = createUser(t, ctx, pool, cleanup, fmt.Sprintf("Racer%d", i))
	}

	var wg sync.WaitGroup
	var successes atomic.Int32
	var unavailable atomic.Int32
	var unexpected atomic.Int32
	for _, uid := range userIDs {
		wg.Add(1)
		go func(userID string) {
			defer wg.Done()
			_, err := repo.RedeemInvite(ctx, code, userID)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, domain.ErrInviteUnavailable):
				unavailable.Add(1)
			default:
				unexpected.Add(1)
				t.Errorf("racer %s: unexpected error: %v", userID, err)
			}
		}(uid)
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("successful joins = %d, want exactly 1 (max_uses=1)", successes.Load())
	}
	if unavailable.Load() != racers-1 {
		t.Fatalf("ErrInviteUnavailable count = %d, want %d", unavailable.Load(), racers-1)
	}
	var useCount int
	if err = pool.QueryRow(ctx, `SELECT use_count FROM group_invites WHERE code=$1`, code).Scan(&useCount); err != nil {
		t.Fatal(err)
	}
	if useCount != 1 {
		t.Fatalf("invite use_count after the race = %d, want exactly 1 (no over-admission)", useCount)
	}
}

// --- Leave / remove member (AC-6, AC-7, AC-8) -----------------------------

func TestLeaveOrRemoveMember_CaptainCanNeverLeaveOrBeRemoved(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	group, captainMembership := mustCreateGroup(t, ctx, repo, cleanup, "Captain Trip", captainID)

	if err := repo.LeaveOrRemoveMember(ctx, group.ID, captainMembership.ID, captainID); !errors.Is(err, domain.ErrCaptainTransferRequired) {
		t.Fatalf("captain self-leave: error = %v, want ErrCaptainTransferRequired", err)
	}
}

// A non-member must never learn a target membership's state through the
// response: neither idempotent success (the membership is inactive) nor
// CAPTAIN_TRANSFER_REQUIRED (the membership is the active Captain). Both
// must resolve to ErrForbidden before that state is ever consulted.
func TestLeaveOrRemoveMember_ForbidsANonMemberFromObservingAnInactiveTargetsState(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberID := createUser(t, ctx, pool, cleanup, "Member")
	outsiderID := createUser(t, ctx, pool, cleanup, "Outsider")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Oracle Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	join, err := repo.RedeemInvite(ctx, code, memberID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.LeaveOrRemoveMember(ctx, group.ID, join.MembershipID, memberID); err != nil {
		t.Fatalf("member leaves legitimately: %v", err)
	}

	// The target is now inactive. A non-member retargeting it must get
	// ErrForbidden, not the idempotent success a real owner would get.
	if err = repo.LeaveOrRemoveMember(ctx, group.ID, join.MembershipID, outsiderID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-member targeting an inactive membership: error = %v, want ErrForbidden", err)
	}
}

func TestLeaveOrRemoveMember_ForbidsANonMemberFromObservingTheCaptainsMembership(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	outsiderID := createUser(t, ctx, pool, cleanup, "Outsider")
	group, captainMembership := mustCreateGroup(t, ctx, repo, cleanup, "Captain Oracle Trip", captainID)

	// A non-member targeting the Captain's membership must get ErrForbidden,
	// not CAPTAIN_TRANSFER_REQUIRED (which would reveal who the Captain is).
	if err := repo.LeaveOrRemoveMember(ctx, group.ID, captainMembership.ID, outsiderID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-member targeting the Captain's membership: error = %v, want ErrForbidden", err)
	}
}

func TestLeaveOrRemoveMember_ForbidsANonCaptainRemovingAnotherMember(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberAID := createUser(t, ctx, pool, cleanup, "MemberA")
	memberBID := createUser(t, ctx, pool, cleanup, "MemberB")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Forbid Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RedeemInvite(ctx, code, memberAID); err != nil {
		t.Fatal(err)
	}
	joinB, err := repo.RedeemInvite(ctx, code, memberBID)
	if err != nil {
		t.Fatal(err)
	}

	if err = repo.LeaveOrRemoveMember(ctx, group.ID, joinB.MembershipID, memberAID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-Captain removes another: error = %v, want ErrForbidden", err)
	}
}

func TestLeaveOrRemoveMember_BlockedByOpenDebtsAsDebtorAndCreditorThenSucceedsOnceSettled(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberID := createUser(t, ctx, pool, cleanup, "Member")
	group, captainMembership := mustCreateGroup(t, ctx, repo, cleanup, "Debt Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	join, err := repo.RedeemInvite(ctx, code, memberID)
	if err != nil {
		t.Fatal(err)
	}

	var billID string
	if err = pool.QueryRow(ctx, `INSERT INTO bills (group_id, creditor_member_id, status, finalized_at) VALUES ($1,$2,'finalized',now()) RETURNING id`, group.ID, captainMembership.ID).Scan(&billID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO debts (group_id, bill_id, debtor_member_id, creditor_member_id, amount, status) VALUES ($1,$2,$3,$4,75000,'awaiting')`, group.ID, billID, join.MembershipID, captainMembership.ID); err != nil {
		t.Fatal(err)
	}

	err = repo.LeaveOrRemoveMember(ctx, group.ID, join.MembershipID, memberID)
	var openDebts *domain.OpenDebtsError
	if !errors.As(err, &openDebts) {
		t.Fatalf("leave with an open debt: error = %v, want *domain.OpenDebtsError", err)
	}
	if openDebts.PayableAmount != 75000 || openDebts.ReceivableAmount != 0 {
		t.Fatalf("unexpected debt totals: %+v", openDebts)
	}

	if _, err = pool.Exec(ctx, `DELETE FROM debts WHERE bill_id=$1`, billID); err != nil {
		t.Fatal(err)
	}
	if err = repo.LeaveOrRemoveMember(ctx, group.ID, join.MembershipID, memberID); err != nil {
		t.Fatalf("leave after settling debt: %v", err)
	}
	var status string
	var leftAt time.Time
	if err = pool.QueryRow(ctx, `SELECT status, left_at FROM group_members WHERE id=$1`, join.MembershipID).Scan(&status, &leftAt); err != nil {
		t.Fatal(err)
	}
	if status != domain.MembershipInactive {
		t.Fatalf("status after leaving = %q, want inactive", status)
	}

	// Idempotent retry: succeeds again, and left_at does not move.
	if err = repo.LeaveOrRemoveMember(ctx, group.ID, join.MembershipID, memberID); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	var leftAtAfterRetry time.Time
	if err = pool.QueryRow(ctx, `SELECT left_at FROM group_members WHERE id=$1`, join.MembershipID).Scan(&leftAtAfterRetry); err != nil {
		t.Fatal(err)
	}
	if !leftAt.Equal(leftAtAfterRetry) {
		t.Fatalf("left_at changed on idempotent retry: %v -> %v", leftAt, leftAtAfterRetry)
	}
}

// TestLeaveOrRemoveMember_VoidedDebtDoesNotBlockExit tái hiện bug: SumOpenDebtorTotal/
// SumOpenCreditorTotal lọc `status <> 'settled'`, nên một khoản nợ đã `voided` (hóa đơn gốc bị
// hủy, Spec 3) vẫn bị tính là "đang mở" và chặn thành viên rời nhóm, dù khoản nợ đó không còn
// nghĩa vụ thật nào (Spec 0002 AC-6, đã sửa sau khi debt_status có thêm giá trị voided ở spec 0003).
func TestLeaveOrRemoveMember_VoidedDebtDoesNotBlockExit(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberID := createUser(t, ctx, pool, cleanup, "Member")
	group, captainMembership := mustCreateGroup(t, ctx, repo, cleanup, "Voided Debt Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	join, err := repo.RedeemInvite(ctx, code, memberID)
	if err != nil {
		t.Fatal(err)
	}

	var billID string
	if err = pool.QueryRow(ctx, `INSERT INTO bills (group_id, creditor_member_id, status, finalized_at, voided_at) VALUES ($1,$2,'voided',now(),now()) RETURNING id`, group.ID, captainMembership.ID).Scan(&billID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO debts (group_id, bill_id, debtor_member_id, creditor_member_id, amount, status, voided_at) VALUES ($1,$2,$3,$4,75000,'voided',now())`, group.ID, billID, join.MembershipID, captainMembership.ID); err != nil {
		t.Fatal(err)
	}

	if err = repo.LeaveOrRemoveMember(ctx, group.ID, join.MembershipID, memberID); err != nil {
		t.Fatalf("leave with only a voided debt should succeed, got: %v", err)
	}
	var status string
	if err = pool.QueryRow(ctx, `SELECT status FROM group_members WHERE id=$1`, join.MembershipID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != domain.MembershipInactive {
		t.Fatalf("status after leaving = %q, want inactive", status)
	}
}

func TestLeaveOrRemoveMember_CaptainRemovesAStandardMemberAndWritesTheActorCorrectly(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberID := createUser(t, ctx, pool, cleanup, "Member")
	group, captainMembership := mustCreateGroup(t, ctx, repo, cleanup, "Removal Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	join, err := repo.RedeemInvite(ctx, code, memberID)
	if err != nil {
		t.Fatal(err)
	}

	if err = repo.LeaveOrRemoveMember(ctx, group.ID, join.MembershipID, captainID); err != nil {
		t.Fatalf("Captain removes member: %v", err)
	}
	var actorID string
	if err = pool.QueryRow(ctx, `SELECT actor_member_id FROM group_activities WHERE group_id=$1 AND action_type='member_removed'`, group.ID).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if actorID != captainMembership.ID {
		t.Fatalf("member_removed actor = %q, want the Captain's membership id %q", actorID, captainMembership.ID)
	}
	meta := activityMetadata(t, ctx, pool, group.ID, "member_removed")
	if meta["target_member_id"] != join.MembershipID {
		t.Fatalf("member_removed metadata = %+v, want target_member_id %q", meta, join.MembershipID)
	}
}

// --- Transfer Captain (AC-7, AC-8) -----------------------------------------

func TestTransferCaptain_HappyPathLeavesExactlyOneActiveCaptain(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberID := createUser(t, ctx, pool, cleanup, "Member")
	group, captainMembership := mustCreateGroup(t, ctx, repo, cleanup, "Transfer Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	join, err := repo.RedeemInvite(ctx, code, memberID)
	if err != nil {
		t.Fatal(err)
	}

	transfer, err := repo.TransferCaptain(ctx, group.ID, join.MembershipID, captainID)
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if transfer.PreviousCaptainMembershipID != captainMembership.ID || transfer.CurrentCaptainMembershipID != join.MembershipID {
		t.Fatalf("unexpected transfer result: %+v", transfer)
	}

	var activeCaptains int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id=$1 AND role='captain' AND status='active'`, group.ID).Scan(&activeCaptains); err != nil {
		t.Fatal(err)
	}
	if activeCaptains != 1 {
		t.Fatalf("active captains after transfer = %d, want exactly 1", activeCaptains)
	}
	var oldRole string
	if err = pool.QueryRow(ctx, `SELECT role FROM group_members WHERE id=$1`, captainMembership.ID).Scan(&oldRole); err != nil {
		t.Fatal(err)
	}
	if oldRole != domain.RoleMember {
		t.Fatalf("old captain's role = %q, want member", oldRole)
	}
}

func TestTransferCaptain_RejectsANonCaptainCaller(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberID := createUser(t, ctx, pool, cleanup, "Member")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Reject Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	join, err := repo.RedeemInvite(ctx, code, memberID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = repo.TransferCaptain(ctx, group.ID, join.MembershipID, memberID); !errors.Is(err, domain.ErrCaptainRequired) {
		t.Fatalf("non-Captain caller: error = %v, want ErrCaptainRequired", err)
	}
}

func TestTransferCaptain_RejectsANonexistentOrInactiveTarget(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberID := createUser(t, ctx, pool, cleanup, "Member")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Missing Target Trip", captainID)

	if _, err := repo.TransferCaptain(ctx, group.ID, "018f0000-0000-7000-8000-000000000000", captainID); !errors.Is(err, domain.ErrMemberNotFound) {
		t.Fatalf("nonexistent target: error = %v, want ErrMemberNotFound", err)
	}

	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	join, err := repo.RedeemInvite(ctx, code, memberID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.LeaveOrRemoveMember(ctx, group.ID, join.MembershipID, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.TransferCaptain(ctx, group.ID, join.MembershipID, captainID); !errors.Is(err, domain.ErrMemberNotFound) {
		t.Fatalf("inactive target: error = %v, want ErrMemberNotFound", err)
	}
}

func TestTransferCaptain_ConcurrentRequestsProduceExactlyOneWinner(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	memberAID := createUser(t, ctx, pool, cleanup, "MemberA")
	memberBID := createUser(t, ctx, pool, cleanup, "MemberB")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Race Transfer Trip", captainID)
	code, err := domain.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	joinA, err := repo.RedeemInvite(ctx, code, memberAID)
	if err != nil {
		t.Fatal(err)
	}
	joinB, err := repo.RedeemInvite(ctx, code, memberBID)
	if err != nil {
		t.Fatal(err)
	}

	// Both goroutines start from a shared barrier so they hit FOR UPDATE
	// NOWAIT as close to simultaneously as possible. Even so, the loser may
	// observe either outcome depending on exactly how much the two
	// transactions overlap: a genuine lock conflict (ErrCaptainTransferConflict)
	// if it arrives while the winner still holds the group lock, or
	// ErrCaptainRequired if it arrives after the winner has already
	// committed and demoted the original Captain. Both are spec-correct;
	// the invariant that must always hold is exactly one winner and exactly
	// one active Captain at the end.
	var wg sync.WaitGroup
	var ready sync.WaitGroup
	var start sync.WaitGroup
	start.Add(1)
	var successes atomic.Int32
	var conflicts atomic.Int32
	var callerNoLongerCaptain atomic.Int32
	var unexpected atomic.Int32
	targets := []string{joinA.MembershipID, joinB.MembershipID}
	ready.Add(len(targets))
	for _, target := range targets {
		wg.Add(1)
		go func(targetID string) {
			defer wg.Done()
			ready.Done()
			start.Wait()
			_, err := repo.TransferCaptain(ctx, group.ID, targetID, captainID)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, domain.ErrCaptainTransferConflict):
				conflicts.Add(1)
			case errors.Is(err, domain.ErrCaptainRequired):
				callerNoLongerCaptain.Add(1)
			default:
				unexpected.Add(1)
				t.Errorf("transfer to %s: unexpected error: %v", targetID, err)
			}
		}(target)
	}
	ready.Wait()
	start.Done()
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("successful transfers = %d, want exactly 1", successes.Load())
	}
	if lost := conflicts.Load() + callerNoLongerCaptain.Load(); lost != 1 {
		t.Fatalf("losing transfer count = %d (conflicts=%d, no-longer-captain=%d), want exactly 1 loser total", lost, conflicts.Load(), callerNoLongerCaptain.Load())
	}
	if unexpected.Load() != 0 {
		t.Fatalf("unexpected error count = %d, see logged errors above", unexpected.Load())
	}
	var activeCaptains int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id=$1 AND role='captain' AND status='active'`, group.ID).Scan(&activeCaptains); err != nil {
		t.Fatal(err)
	}
	if activeCaptains != 1 {
		t.Fatalf("active captains after the race = %d, want exactly 1", activeCaptains)
	}
}

// --- Activity timeline (AC-8) ----------------------------------------------

func TestListActivities_CursorPaginationAndForbiddenForNonMember(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	outsiderID := createUser(t, ctx, pool, cleanup, "Outsider")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Timeline Trip", captainID)
	// group_created already wrote one activity; add two invite activities so
	// there are at least three rows to paginate over.
	for i := 0; i < 2; i++ {
		code, err := domain.NewInviteCode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{GroupID: group.ID, CallerUserID: captainID, Code: code, ExpiresIn: 24 * time.Hour, Regenerate: true}); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]bool{}
	var cursor *string
	for page := 0; page < 10; page++ {
		items, next, err := repo.ListActivities(ctx, repository.ListActivitiesParams{GroupID: group.ID, CallerUserID: captainID, Cursor: cursor, Limit: 1})
		if err != nil {
			t.Fatalf("list activities page %d: %v", page, err)
		}
		if len(items) != 1 {
			t.Fatalf("page %d: got %d items, want 1", page, len(items))
		}
		if seen[items[0].ID] {
			t.Fatalf("activity %s returned twice across pages", items[0].ID)
		}
		seen[items[0].ID] = true
		if next == nil {
			break
		}
		cursor = next
	}
	if len(seen) < 3 {
		t.Fatalf("paginated through %d activities, want at least 3 (group_created + 2 invite_created)", len(seen))
	}

	if _, _, err := repo.ListActivities(ctx, repository.ListActivitiesParams{GroupID: group.ID, CallerUserID: outsiderID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("nonmember: error = %v, want ErrForbidden", err)
	}
}

func TestListActivities_RejectsAnUndecodableCursor(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cleanup := newCleanup(t, pool)
	repo := New(pool)

	captainID := createUser(t, ctx, pool, cleanup, "Captain")
	group, _ := mustCreateGroup(t, ctx, repo, cleanup, "Bad Cursor Trip", captainID)
	badCursor := "garbage"
	if _, _, err := repo.ListActivities(ctx, repository.ListActivitiesParams{GroupID: group.ID, CallerUserID: captainID, Cursor: &badCursor}); !errors.Is(err, domain.ErrInvalidCursor) {
		t.Fatalf("error = %v, want ErrInvalidCursor", err)
	}
}
