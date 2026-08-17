package usecase

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"paysplit-backend/internal/modules/group/domain"
	"paysplit-backend/internal/modules/group/repository"
)

// fakeRepo implements repository.Repository. Each method fails the test if
// called without its function set, so a test that expects validation to
// short circuit before reaching the repository catches a regression where
// it doesn't.
type fakeRepo struct {
	t *testing.T

	createGroupFn         func(context.Context, repository.CreateGroupParams) (*domain.Group, *domain.Membership, error)
	listGroupsFn          func(context.Context, repository.ListGroupsParams) ([]domain.GroupListItem, *string, error)
	getGroupDetailFn      func(ctx context.Context, groupID, callerUserID string) (*domain.GroupDetail, error)
	createOrReuseInviteFn func(context.Context, repository.CreateInviteParams) (*repository.CreateInviteResult, error)
	revokeInviteFn        func(ctx context.Context, groupID, inviteID, callerUserID string) error
	previewInviteFn       func(ctx context.Context, code string) (*domain.InvitePreview, error)
	redeemInviteFn        func(ctx context.Context, code, callerUserID string) (*domain.JoinResult, error)
	leaveOrRemoveMemberFn func(ctx context.Context, groupID, targetMembershipID, callerUserID string) error
	transferCaptainFn     func(ctx context.Context, groupID, targetMembershipID, callerUserID string) (*domain.CaptainTransfer, error)
	listActivitiesFn      func(context.Context, repository.ListActivitiesParams) ([]domain.Activity, *string, error)
}

func (f *fakeRepo) CreateGroup(ctx context.Context, p repository.CreateGroupParams) (*domain.Group, *domain.Membership, error) {
	if f.createGroupFn == nil {
		f.t.Fatal("CreateGroup called but not expected: validation should have short-circuited")
	}
	return f.createGroupFn(ctx, p)
}
func (f *fakeRepo) ListGroups(ctx context.Context, p repository.ListGroupsParams) ([]domain.GroupListItem, *string, error) {
	if f.listGroupsFn == nil {
		f.t.Fatal("ListGroups called but not expected: validation should have short-circuited")
	}
	return f.listGroupsFn(ctx, p)
}
func (f *fakeRepo) GetGroupDetail(ctx context.Context, groupID, callerUserID string) (*domain.GroupDetail, error) {
	if f.getGroupDetailFn == nil {
		f.t.Fatal("GetGroupDetail called but not expected: validation should have short-circuited")
	}
	return f.getGroupDetailFn(ctx, groupID, callerUserID)
}
func (f *fakeRepo) CreateOrReuseInvite(ctx context.Context, p repository.CreateInviteParams) (*repository.CreateInviteResult, error) {
	if f.createOrReuseInviteFn == nil {
		f.t.Fatal("CreateOrReuseInvite called but not expected: validation should have short-circuited")
	}
	return f.createOrReuseInviteFn(ctx, p)
}
func (f *fakeRepo) RevokeInvite(ctx context.Context, groupID, inviteID, callerUserID string) error {
	if f.revokeInviteFn == nil {
		f.t.Fatal("RevokeInvite called but not expected: validation should have short-circuited")
	}
	return f.revokeInviteFn(ctx, groupID, inviteID, callerUserID)
}
func (f *fakeRepo) PreviewInvite(ctx context.Context, code string) (*domain.InvitePreview, error) {
	if f.previewInviteFn == nil {
		f.t.Fatal("PreviewInvite called but not expected: validation should have short-circuited")
	}
	return f.previewInviteFn(ctx, code)
}
func (f *fakeRepo) RedeemInvite(ctx context.Context, code, callerUserID string) (*domain.JoinResult, error) {
	if f.redeemInviteFn == nil {
		f.t.Fatal("RedeemInvite called but not expected: validation should have short-circuited")
	}
	return f.redeemInviteFn(ctx, code, callerUserID)
}
func (f *fakeRepo) LeaveOrRemoveMember(ctx context.Context, groupID, targetMembershipID, callerUserID string) error {
	if f.leaveOrRemoveMemberFn == nil {
		f.t.Fatal("LeaveOrRemoveMember called but not expected: validation should have short-circuited")
	}
	return f.leaveOrRemoveMemberFn(ctx, groupID, targetMembershipID, callerUserID)
}
func (f *fakeRepo) TransferCaptain(ctx context.Context, groupID, targetMembershipID, callerUserID string) (*domain.CaptainTransfer, error) {
	if f.transferCaptainFn == nil {
		f.t.Fatal("TransferCaptain called but not expected: validation should have short-circuited")
	}
	return f.transferCaptainFn(ctx, groupID, targetMembershipID, callerUserID)
}
func (f *fakeRepo) ListActivities(ctx context.Context, p repository.ListActivitiesParams) ([]domain.Activity, *string, error) {
	if f.listActivitiesFn == nil {
		f.t.Fatal("ListActivities called but not expected: validation should have short-circuited")
	}
	return f.listActivitiesFn(ctx, p)
}

func newTestService(t *testing.T, repo *fakeRepo) *Service {
	t.Helper()
	repo.t = t
	return NewService(repo, "paysplit://join")
}

// --- NewService ---

func TestNewService_PanicsOnNilRepository(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for a nil repository")
		}
	}()
	NewService(nil, "paysplit://join")
}

func TestNewService_PanicsOnEmptyInviteURLBase(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for an empty invite URL base")
		}
	}()
	NewService(&fakeRepo{}, "   ")
}

// --- CreateGroup (AC-1) ---

func TestCreateGroup_TrimsNameAndDefaultsCurrencyToVND(t *testing.T) {
	repo := &fakeRepo{createGroupFn: func(_ context.Context, p repository.CreateGroupParams) (*domain.Group, *domain.Membership, error) {
		if p.Name != "Du lịch Đà Lạt" {
			t.Fatalf("repo received name %q, want trimmed %q", p.Name, "Du lịch Đà Lạt")
		}
		if p.Currency != "VND" {
			t.Fatalf("repo received currency %q, want default VND", p.Currency)
		}
		if p.CreatedBy != "user-1" {
			t.Fatalf("repo received CreatedBy %q, want %q", p.CreatedBy, "user-1")
		}
		return &domain.Group{ID: "group-1", Name: p.Name, Currency: p.Currency}, &domain.Membership{ID: "member-1", Role: domain.RoleCaptain}, nil
	}}
	svc := newTestService(t, repo)

	out, err := svc.CreateGroup(context.Background(), CreateGroupInput{Name: "  Du lịch Đà Lạt  ", CreatedBy: "user-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Group.ID != "group-1" || out.Membership.ID != "member-1" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestCreateGroup_RejectsBlankNameAfterTrim(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	_, err := svc.CreateGroup(context.Background(), CreateGroupInput{Name: "   ", CreatedBy: "user-1"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateGroup_RejectsNameOverOneHundredCodePoints(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	_, err := svc.CreateGroup(context.Background(), CreateGroupInput{Name: strings.Repeat("a", 101), CreatedBy: "user-1"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateGroup_AcceptsNameAtTheHundredCodePointBoundary(t *testing.T) {
	repo := &fakeRepo{createGroupFn: func(_ context.Context, p repository.CreateGroupParams) (*domain.Group, *domain.Membership, error) {
		return &domain.Group{ID: "group-1"}, &domain.Membership{ID: "member-1"}, nil
	}}
	svc := newTestService(t, repo)
	_, err := svc.CreateGroup(context.Background(), CreateGroupInput{Name: strings.Repeat("a", 100), CreatedBy: "user-1"})
	if err != nil {
		t.Fatalf("unexpected error at the 100 code point boundary: %v", err)
	}
}

func TestCreateGroup_CountsUnicodeCodePointsNotBytes(t *testing.T) {
	// 50 Vietnamese "à" characters are 100 bytes (2 bytes each in UTF-8) but
	// only 50 code points, well inside the 100 code point limit.
	repo := &fakeRepo{createGroupFn: func(_ context.Context, p repository.CreateGroupParams) (*domain.Group, *domain.Membership, error) {
		return &domain.Group{ID: "group-1"}, &domain.Membership{ID: "member-1"}, nil
	}}
	svc := newTestService(t, repo)
	name := strings.Repeat("à", 50)
	if len(name) != 100 {
		t.Fatalf("test setup: expected 100 bytes, got %d", len(name))
	}
	_, err := svc.CreateGroup(context.Background(), CreateGroupInput{Name: name, CreatedBy: "user-1"})
	if err != nil {
		t.Fatalf("unexpected error for a byte-long but code-point-short name: %v", err)
	}
}

func TestCreateGroup_RejectsNonVNDCurrency(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	_, err := svc.CreateGroup(context.Background(), CreateGroupInput{Name: "Trip", Currency: "USD", CreatedBy: "user-1"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateGroup_RejectsBlankCreatedBy(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	_, err := svc.CreateGroup(context.Background(), CreateGroupInput{Name: "Trip", CreatedBy: "  "})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateGroup_PropagatesRepositoryError(t *testing.T) {
	sentinel := errors.New("db unavailable")
	repo := &fakeRepo{createGroupFn: func(context.Context, repository.CreateGroupParams) (*domain.Group, *domain.Membership, error) {
		return nil, nil, sentinel
	}}
	svc := newTestService(t, repo)
	_, err := svc.CreateGroup(context.Background(), CreateGroupInput{Name: "Trip", CreatedBy: "user-1"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the repository's sentinel error", err)
	}
}

// --- ListGroups (AC-2) ---

func TestListGroups_RejectsBlankUserID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	_, err := svc.ListGroups(context.Background(), ListGroupsInput{UserID: " "})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestListGroups_PassesThroughToRepository(t *testing.T) {
	cursor := "some-cursor"
	repo := &fakeRepo{listGroupsFn: func(_ context.Context, p repository.ListGroupsParams) ([]domain.GroupListItem, *string, error) {
		if p.UserID != "user-1" || p.Cursor == nil || *p.Cursor != cursor {
			t.Fatalf("unexpected params: %+v", p)
		}
		if p.Limit != 5 {
			t.Fatalf("limit = %d, want 5", p.Limit)
		}
		next := "next-cursor"
		return []domain.GroupListItem{{CallerRole: "captain"}}, &next, nil
	}}
	svc := newTestService(t, repo)
	out, err := svc.ListGroups(context.Background(), ListGroupsInput{UserID: "user-1", Cursor: &cursor, Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Items) != 1 || out.NextCursor == nil || *out.NextCursor != "next-cursor" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

// --- GetGroupDetail (AC-2) ---

func TestGetGroupDetail_RejectsBlankGroupIDOrCallerID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if _, err := svc.GetGroupDetail(context.Background(), "", "user-1"); !errors.Is(err, domain.ErrGroupNotFound) {
		t.Fatalf("blank groupID: error = %v, want ErrGroupNotFound", err)
	}
	if _, err := svc.GetGroupDetail(context.Background(), "group-1", ""); !errors.Is(err, domain.ErrGroupNotFound) {
		t.Fatalf("blank callerUserID: error = %v, want ErrGroupNotFound", err)
	}
}

func TestGetGroupDetail_PassesThroughToRepository(t *testing.T) {
	repo := &fakeRepo{getGroupDetailFn: func(_ context.Context, groupID, callerUserID string) (*domain.GroupDetail, error) {
		return &domain.GroupDetail{CallerRole: "member"}, nil
	}}
	svc := newTestService(t, repo)
	out, err := svc.GetGroupDetail(context.Background(), "group-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.CallerRole != "member" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

// --- CreateInvite (AC-3) ---

func TestCreateInvite_RejectsBlankGroupIDOrCallerID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if _, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "", CallerUserID: "user-1"}); !errors.Is(err, domain.ErrCaptainRequired) {
		t.Fatalf("blank groupID: error = %v, want ErrCaptainRequired", err)
	}
	if _, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "group-1", CallerUserID: ""}); !errors.Is(err, domain.ErrCaptainRequired) {
		t.Fatalf("blank callerUserID: error = %v, want ErrCaptainRequired", err)
	}
}

func TestCreateInvite_DefaultsExpiryToTwentyFourHours(t *testing.T) {
	repo := &fakeRepo{createOrReuseInviteFn: func(_ context.Context, p repository.CreateInviteParams) (*repository.CreateInviteResult, error) {
		if p.ExpiresIn.Hours() != 24 {
			t.Fatalf("ExpiresIn = %v, want 24h default", p.ExpiresIn)
		}
		return &repository.CreateInviteResult{Invite: &domain.Invite{Code: "abc"}, Created: true}, nil
	}}
	svc := newTestService(t, repo)
	_, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "group-1", CallerUserID: "user-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateInvite_RejectsExpiresInHoursOutOfRange(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	tooLow, tooHigh := 0, 169
	if _, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "g", CallerUserID: "u", ExpiresInHours: &tooLow}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expires_in_hours=0: error = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "g", CallerUserID: "u", ExpiresInHours: &tooHigh}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expires_in_hours=169: error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateInvite_AcceptsExpiresInHoursAtBoundaries(t *testing.T) {
	for _, hours := range []int{1, 168} {
		hours := hours
		repo := &fakeRepo{createOrReuseInviteFn: func(_ context.Context, p repository.CreateInviteParams) (*repository.CreateInviteResult, error) {
			return &repository.CreateInviteResult{Invite: &domain.Invite{Code: "abc"}}, nil
		}}
		svc := newTestService(t, repo)
		if _, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "g", CallerUserID: "u", ExpiresInHours: &hours}); err != nil {
			t.Fatalf("expires_in_hours=%d: unexpected error: %v", hours, err)
		}
	}
}

func TestCreateInvite_RejectsMaxUsesOutOfRange(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	tooLow, tooHigh := 0, 51
	if _, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "g", CallerUserID: "u", MaxUses: &tooLow}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("max_uses=0: error = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "g", CallerUserID: "u", MaxUses: &tooHigh}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("max_uses=51: error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateInvite_NilMaxUsesMeansUnlimited(t *testing.T) {
	repo := &fakeRepo{createOrReuseInviteFn: func(_ context.Context, p repository.CreateInviteParams) (*repository.CreateInviteResult, error) {
		if p.MaxUses != nil {
			t.Fatalf("MaxUses = %v, want nil", p.MaxUses)
		}
		return &repository.CreateInviteResult{Invite: &domain.Invite{Code: "abc"}}, nil
	}}
	svc := newTestService(t, repo)
	if _, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "g", CallerUserID: "u"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateInvite_BuildsInviteURLFromBaseAndCode(t *testing.T) {
	repo := &fakeRepo{createOrReuseInviteFn: func(_ context.Context, p repository.CreateInviteParams) (*repository.CreateInviteResult, error) {
		return &repository.CreateInviteResult{Invite: &domain.Invite{Code: "the-code"}, Created: true}, nil
	}}
	svc := newTestService(t, repo)
	out, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "g", CallerUserID: "u"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.InviteURL != "paysplit://join?code=the-code" {
		t.Fatalf("InviteURL = %q, want the code set as a query parameter on the base", out.InviteURL)
	}
	if !out.Created {
		t.Fatal("Created = false, want true (passthrough from repository result)")
	}
}

func TestCreateInvite_InviteURLIsParseableWithTheCodeRecoverable(t *testing.T) {
	// A regression guard for the bare-concatenation bug: whatever the base
	// looks like, the result must be a real URL a client can parse back
	// apart, not a mangled string with the code glued onto the host.
	for _, base := range []string{"paysplit://join", "https://app.paysplit.dev/invite", "paysplit://join?utm_source=share"} {
		repo := &fakeRepo{createOrReuseInviteFn: func(_ context.Context, p repository.CreateInviteParams) (*repository.CreateInviteResult, error) {
			return &repository.CreateInviteResult{Invite: &domain.Invite{Code: "AbC-123_xyz"}, Created: true}, nil
		}}
		svc := NewService(repo, base)
		repo.t = t
		out, err := svc.CreateInvite(context.Background(), CreateInviteInput{GroupID: "g", CallerUserID: "u"})
		if err != nil {
			t.Fatalf("base %q: unexpected error: %v", base, err)
		}
		parsed, err := url.Parse(out.InviteURL)
		if err != nil {
			t.Fatalf("base %q: InviteURL %q is not a parseable URL: %v", base, out.InviteURL, err)
		}
		if got := parsed.Query().Get("code"); got != "AbC-123_xyz" {
			t.Fatalf("base %q: InviteURL %q does not carry the code back out as a query parameter, got %q", base, out.InviteURL, got)
		}
	}
}

// --- RevokeInvite (AC-3) ---

func TestRevokeInvite_RejectsBlankGroupIDOrCallerID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if err := svc.RevokeInvite(context.Background(), "", "invite-1", "user-1"); !errors.Is(err, domain.ErrCaptainRequired) {
		t.Fatalf("blank groupID: error = %v, want ErrCaptainRequired", err)
	}
	if err := svc.RevokeInvite(context.Background(), "group-1", "invite-1", ""); !errors.Is(err, domain.ErrCaptainRequired) {
		t.Fatalf("blank callerUserID: error = %v, want ErrCaptainRequired", err)
	}
}

func TestRevokeInvite_RejectsBlankInviteID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if err := svc.RevokeInvite(context.Background(), "group-1", "  ", "user-1"); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("error = %v, want ErrInviteNotFound", err)
	}
}

// --- PreviewInvite (AC-4) ---

func TestPreviewInvite_RejectsBlankCode(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if _, err := svc.PreviewInvite(context.Background(), " "); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("error = %v, want ErrInviteNotFound", err)
	}
}

// --- JoinGroup (AC-5) ---

func TestJoinGroup_RejectsBlankCodeOrCallerID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if _, err := svc.JoinGroup(context.Background(), "", "user-1"); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("blank code: error = %v, want ErrInviteNotFound", err)
	}
	if _, err := svc.JoinGroup(context.Background(), "code-1", ""); !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("blank callerUserID: error = %v, want ErrInviteNotFound", err)
	}
}

func TestJoinGroup_PassesThroughResult(t *testing.T) {
	repo := &fakeRepo{redeemInviteFn: func(_ context.Context, code, callerUserID string) (*domain.JoinResult, error) {
		return &domain.JoinResult{Result: domain.JoinResultAlreadyActive, MembershipID: "member-1"}, nil
	}}
	svc := newTestService(t, repo)
	out, err := svc.JoinGroup(context.Background(), "code-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Result != domain.JoinResultAlreadyActive {
		t.Fatalf("Result = %q, want %q", out.Result, domain.JoinResultAlreadyActive)
	}
}

// --- LeaveOrRemoveMember (AC-6, AC-7) ---

func TestLeaveOrRemoveMember_RejectsBlankGroupIDOrCallerID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if err := svc.LeaveOrRemoveMember(context.Background(), "", "member-1", "user-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("blank groupID: error = %v, want ErrForbidden", err)
	}
	if err := svc.LeaveOrRemoveMember(context.Background(), "group-1", "member-1", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("blank callerUserID: error = %v, want ErrForbidden", err)
	}
}

func TestLeaveOrRemoveMember_RejectsBlankTargetMembershipID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if err := svc.LeaveOrRemoveMember(context.Background(), "group-1", " ", "user-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestLeaveOrRemoveMember_PropagatesOpenDebtsError(t *testing.T) {
	sentinel := &domain.OpenDebtsError{PayableAmount: 1000, ReceivableAmount: 0}
	repo := &fakeRepo{leaveOrRemoveMemberFn: func(context.Context, string, string, string) error {
		return sentinel
	}}
	svc := newTestService(t, repo)
	err := svc.LeaveOrRemoveMember(context.Background(), "group-1", "member-1", "user-1")
	var got *domain.OpenDebtsError
	if !errors.As(err, &got) {
		t.Fatalf("error = %v, want an *domain.OpenDebtsError", err)
	}
	if got.PayableAmount != 1000 {
		t.Fatalf("PayableAmount = %d, want 1000", got.PayableAmount)
	}
}

// --- TransferCaptain (AC-7) ---

func TestTransferCaptain_RejectsBlankGroupIDOrCallerID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if _, err := svc.TransferCaptain(context.Background(), "", "member-1", "user-1"); !errors.Is(err, domain.ErrCaptainRequired) {
		t.Fatalf("blank groupID: error = %v, want ErrCaptainRequired", err)
	}
	if _, err := svc.TransferCaptain(context.Background(), "group-1", "member-1", ""); !errors.Is(err, domain.ErrCaptainRequired) {
		t.Fatalf("blank callerUserID: error = %v, want ErrCaptainRequired", err)
	}
}

func TestTransferCaptain_RejectsBlankTargetMembershipID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if _, err := svc.TransferCaptain(context.Background(), "group-1", "  ", "user-1"); !errors.Is(err, domain.ErrMemberNotFound) {
		t.Fatalf("error = %v, want ErrMemberNotFound", err)
	}
}

func TestTransferCaptain_PassesThroughToRepository(t *testing.T) {
	repo := &fakeRepo{transferCaptainFn: func(context.Context, string, string, string) (*domain.CaptainTransfer, error) {
		return &domain.CaptainTransfer{PreviousCaptainMembershipID: "old", CurrentCaptainMembershipID: "new"}, nil
	}}
	svc := newTestService(t, repo)
	out, err := svc.TransferCaptain(context.Background(), "group-1", "member-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.PreviousCaptainMembershipID != "old" || out.CurrentCaptainMembershipID != "new" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

// --- ListActivities (AC-8) ---

func TestListActivities_RejectsBlankGroupIDOrCallerID(t *testing.T) {
	svc := newTestService(t, &fakeRepo{})
	if _, err := svc.ListActivities(context.Background(), ListActivitiesInput{GroupID: "", CallerUserID: "user-1"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("blank groupID: error = %v, want ErrForbidden", err)
	}
	if _, err := svc.ListActivities(context.Background(), ListActivitiesInput{GroupID: "group-1", CallerUserID: ""}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("blank callerUserID: error = %v, want ErrForbidden", err)
	}
}

func TestListActivities_PassesThroughToRepository(t *testing.T) {
	repo := &fakeRepo{listActivitiesFn: func(_ context.Context, p repository.ListActivitiesParams) ([]domain.Activity, *string, error) {
		return []domain.Activity{{ActionType: "group_created"}}, nil, nil
	}}
	svc := newTestService(t, repo)
	out, err := svc.ListActivities(context.Background(), ListActivitiesInput{GroupID: "group-1", CallerUserID: "user-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ActionType != "group_created" {
		t.Fatalf("unexpected output: %+v", out)
	}
}
