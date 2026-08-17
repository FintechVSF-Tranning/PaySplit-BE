package usecase

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"paysplit-backend/internal/modules/group/domain"
	"paysplit-backend/internal/modules/group/repository"
)

const (
	minGroupNameLength = 1
	maxGroupNameLength = 100
	defaultCurrency    = "VND"

	defaultInviteExpiresInHours = 24
	minInviteExpiresInHours     = 1
	maxInviteExpiresInHours     = 168
	minInviteMaxUses            = 1
	maxInviteMaxUses            = 50
)

type Service struct {
	repo          repository.Repository
	inviteURLBase string
}

func NewService(repo repository.Repository, inviteURLBase string) *Service {
	if repo == nil {
		panic("group service repository must not be nil")
	}
	if strings.TrimSpace(inviteURLBase) == "" {
		panic("group service invite URL base must not be empty")
	}
	return &Service{repo: repo, inviteURLBase: inviteURLBase}
}

type CreateGroupInput struct {
	Name      string
	Currency  string
	CreatedBy string
}

type CreateGroupOutput struct {
	Group      *domain.Group
	Membership *domain.Membership
}

// CreateGroup applies the AC-1 validation contract: strings.TrimSpace, 1 to
// 100 Unicode code points, and only VND accepted in v1.
func (s *Service) CreateGroup(ctx context.Context, in CreateGroupInput) (*CreateGroupOutput, error) {
	name := strings.TrimSpace(in.Name)
	if length := utf8.RuneCountInString(name); length < minGroupNameLength || length > maxGroupNameLength {
		return nil, domain.ErrInvalidInput
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = defaultCurrency
	}
	if currency != defaultCurrency {
		return nil, domain.ErrInvalidInput
	}
	if strings.TrimSpace(in.CreatedBy) == "" {
		return nil, domain.ErrInvalidInput
	}

	group, membership, err := s.repo.CreateGroup(ctx, repository.CreateGroupParams{Name: name, Currency: currency, CreatedBy: in.CreatedBy})
	if err != nil {
		return nil, err
	}
	return &CreateGroupOutput{Group: group, Membership: membership}, nil
}

type ListGroupsInput struct {
	UserID string
	Cursor *string
	Limit  int
}

type ListGroupsOutput struct {
	Items      []domain.GroupListItem
	NextCursor *string
}

func (s *Service) ListGroups(ctx context.Context, in ListGroupsInput) (*ListGroupsOutput, error) {
	if strings.TrimSpace(in.UserID) == "" {
		return nil, domain.ErrInvalidInput
	}
	items, nextCursor, err := s.repo.ListGroups(ctx, repository.ListGroupsParams{UserID: in.UserID, Cursor: in.Cursor, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	return &ListGroupsOutput{Items: items, NextCursor: nextCursor}, nil
}

// GetGroupDetail returns the group, its active members, balances, and the
// caller's role. A missing group and a nonmember caller both surface as
// domain.ErrGroupNotFound so the endpoint never reveals group existence.
func (s *Service) GetGroupDetail(ctx context.Context, groupID, callerUserID string) (*domain.GroupDetail, error) {
	if strings.TrimSpace(groupID) == "" || strings.TrimSpace(callerUserID) == "" {
		return nil, domain.ErrGroupNotFound
	}
	return s.repo.GetGroupDetail(ctx, groupID, callerUserID)
}

type CreateInviteInput struct {
	GroupID        string
	CallerUserID   string
	ExpiresInHours *int
	MaxUses        *int
	Regenerate     bool
}

type CreateInviteOutput struct {
	Invite    *domain.Invite
	InviteURL string
	Created   bool
}

// CreateInvite applies the AC-3 validation contract: expiry defaults to 24
// hours and accepts 1 through 168, max_uses is optional and accepts 1
// through 50. Without regeneration the group's one available invite is
// reused; regeneration revokes every available invite first.
func (s *Service) CreateInvite(ctx context.Context, in CreateInviteInput) (*CreateInviteOutput, error) {
	if strings.TrimSpace(in.GroupID) == "" || strings.TrimSpace(in.CallerUserID) == "" {
		return nil, domain.ErrCaptainRequired
	}
	expiresInHours := defaultInviteExpiresInHours
	if in.ExpiresInHours != nil {
		expiresInHours = *in.ExpiresInHours
	}
	if expiresInHours < minInviteExpiresInHours || expiresInHours > maxInviteExpiresInHours {
		return nil, domain.ErrInvalidInput
	}
	if in.MaxUses != nil && (*in.MaxUses < minInviteMaxUses || *in.MaxUses > maxInviteMaxUses) {
		return nil, domain.ErrInvalidInput
	}

	code, err := domain.NewInviteCode()
	if err != nil {
		return nil, fmt.Errorf("generate invite code: %w", err)
	}
	result, err := s.repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{
		GroupID:      in.GroupID,
		CallerUserID: in.CallerUserID,
		Code:         code,
		ExpiresIn:    time.Duration(expiresInHours) * time.Hour,
		MaxUses:      in.MaxUses,
		Regenerate:   in.Regenerate,
	})
	if err != nil {
		return nil, err
	}
	inviteURL, err := buildInviteURL(s.inviteURLBase, result.Invite.Code)
	if err != nil {
		return nil, fmt.Errorf("build invite URL: %w", err)
	}
	return &CreateInviteOutput{Invite: result.Invite, InviteURL: inviteURL, Created: result.Created}, nil
}

// buildInviteURL sets code as a query parameter on the configured base,
// mirroring gmail.tokenURL's pattern for the auth module's own deep links.
// Bare concatenation ("paysplit://join" + code) produced an unroutable
// link with no separator between the base and the code.
func buildInviteURL(base, code string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse invite URL base: %w", err)
	}
	query := parsed.Query()
	query.Set("code", code)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// RevokeInvite is idempotent: revoking an already revoked or unknown-state
// invite still succeeds once the caller and invite are confirmed valid.
func (s *Service) RevokeInvite(ctx context.Context, groupID, inviteID, callerUserID string) error {
	if strings.TrimSpace(groupID) == "" || strings.TrimSpace(callerUserID) == "" {
		return domain.ErrCaptainRequired
	}
	if strings.TrimSpace(inviteID) == "" {
		return domain.ErrInviteNotFound
	}
	return s.repo.RevokeInvite(ctx, groupID, inviteID, callerUserID)
}

// PreviewInvite lets an authenticated nonmember see the group name, active
// member count, and Captain display name for a valid invite code (AC-4).
func (s *Service) PreviewInvite(ctx context.Context, code string) (*domain.InvitePreview, error) {
	if strings.TrimSpace(code) == "" {
		return nil, domain.ErrInviteNotFound
	}
	return s.repo.PreviewInvite(ctx, code)
}

// JoinGroup redeems an invite code: a new join or reactivation validates
// the invite, enforces capacity, and activates the membership; an already
// active member gets idempotent success (AC-5).
func (s *Service) JoinGroup(ctx context.Context, code, callerUserID string) (*domain.JoinResult, error) {
	if strings.TrimSpace(code) == "" {
		return nil, domain.ErrInviteNotFound
	}
	if strings.TrimSpace(callerUserID) == "" {
		return nil, domain.ErrInviteNotFound
	}
	return s.repo.RedeemInvite(ctx, code, callerUserID)
}

// LeaveOrRemoveMember lets an active member leave their own membership, or
// the active Captain remove another standard member. It is idempotent and
// rejects a target with any unsettled debts (AC-6), and never lets the
// active Captain leave or be removed (AC-7).
func (s *Service) LeaveOrRemoveMember(ctx context.Context, groupID, targetMembershipID, callerUserID string) error {
	if strings.TrimSpace(groupID) == "" || strings.TrimSpace(callerUserID) == "" {
		return domain.ErrForbidden
	}
	if strings.TrimSpace(targetMembershipID) == "" {
		return domain.ErrForbidden
	}
	return s.repo.LeaveOrRemoveMember(ctx, groupID, targetMembershipID, callerUserID)
}

// TransferCaptain moves the Captain role to another active member; only
// the active Captain may call it (AC-7).
func (s *Service) TransferCaptain(ctx context.Context, groupID, targetMembershipID, callerUserID string) (*domain.CaptainTransfer, error) {
	if strings.TrimSpace(groupID) == "" || strings.TrimSpace(callerUserID) == "" {
		return nil, domain.ErrCaptainRequired
	}
	if strings.TrimSpace(targetMembershipID) == "" {
		return nil, domain.ErrMemberNotFound
	}
	return s.repo.TransferCaptain(ctx, groupID, targetMembershipID, callerUserID)
}

type ListActivitiesInput struct {
	GroupID      string
	CallerUserID string
	Cursor       *string
	Limit        int
}

type ListActivitiesOutput struct {
	Items      []domain.Activity
	NextCursor *string
}

// ListActivities reads a group's timeline ordered by (created_at, id)
// descending; only an active member may read it (AC-8).
func (s *Service) ListActivities(ctx context.Context, in ListActivitiesInput) (*ListActivitiesOutput, error) {
	if strings.TrimSpace(in.GroupID) == "" || strings.TrimSpace(in.CallerUserID) == "" {
		return nil, domain.ErrForbidden
	}
	items, nextCursor, err := s.repo.ListActivities(ctx, repository.ListActivitiesParams{GroupID: in.GroupID, CallerUserID: in.CallerUserID, Cursor: in.Cursor, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	return &ListActivitiesOutput{Items: items, NextCursor: nextCursor}, nil
}
