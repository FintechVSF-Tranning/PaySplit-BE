package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
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
	maxInviteCodeAttempts       = 5
)

var inviteCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)

type Service struct {
	repo          repository.Repository
	inviteURLBase string
	newInviteCode func() (string, error)
}

func NewService(repo repository.Repository, inviteURLBase string) *Service {
	if repo == nil {
		panic("group service repository must not be nil")
	}
	normalizedBase, err := normalizeInviteURLBase(inviteURLBase)
	if err != nil {
		panic(fmt.Sprintf("group service invite URL base is invalid: %v", err))
	}
	return &Service{repo: repo, inviteURLBase: normalizedBase, newInviteCode: domain.NewInviteCode}
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
	GroupID              string
	CallerUserID         string
	ExpiresInHours       *int
	MaxUses              *int
	Regenerate           *bool
	HasConfiguration     bool
	HasNullConfiguration bool
}

type CreateInviteOutput struct {
	Invite    *domain.Invite
	InviteURL string
	Created   bool
}

// AuthorizeInviteConfiguration is the presence-first authorization used by
// HTTP before it decodes policy values. CreateInvite repeats the check before
// mutation, and the repository checks again under the group lock.
func (s *Service) AuthorizeInviteConfiguration(ctx context.Context, groupID, callerUserID string) error {
	if strings.TrimSpace(groupID) == "" || strings.TrimSpace(callerUserID) == "" {
		return domain.ErrGroupNotFound
	}
	role, err := s.repo.GetActiveMembershipRole(ctx, groupID, callerUserID)
	if err != nil {
		return err
	}
	if role != domain.RoleCaptain {
		return domain.ErrCaptainRequired
	}
	return nil
}

// CreateInvite applies the AC-3 validation contract: expiry defaults to 24
// hours and accepts 1 through 168, max_uses is optional and accepts 1
// through 50. Without regeneration the group's one available invite is
// reused; regeneration revokes every available invite first.
func (s *Service) CreateInvite(ctx context.Context, in CreateInviteInput) (*CreateInviteOutput, error) {
	if strings.TrimSpace(in.GroupID) == "" || strings.TrimSpace(in.CallerUserID) == "" {
		return nil, domain.ErrGroupNotFound
	}
	var err error
	if in.HasConfiguration {
		err = s.AuthorizeInviteConfiguration(ctx, in.GroupID, in.CallerUserID)
	} else {
		_, err = s.repo.GetActiveMembershipRole(ctx, in.GroupID, in.CallerUserID)
	}
	if err != nil {
		return nil, err
	}
	if in.HasNullConfiguration {
		return nil, domain.ErrInvalidInput
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

	regenerate := false
	if in.Regenerate != nil {
		regenerate = *in.Regenerate
	}

	var result *repository.CreateInviteResult
	for attempt := 0; attempt < maxInviteCodeAttempts; attempt++ {
		code, generateErr := s.newInviteCode()
		if generateErr != nil {
			return nil, fmt.Errorf("generate invite code: %w", generateErr)
		}
		result, err = s.repo.CreateOrReuseInvite(ctx, repository.CreateInviteParams{
			GroupID:          in.GroupID,
			CallerUserID:     in.CallerUserID,
			Code:             code,
			ExpiresIn:        time.Duration(expiresInHours) * time.Hour,
			MaxUses:          in.MaxUses,
			Regenerate:       regenerate,
			HasConfiguration: in.HasConfiguration,
		})
		if !errors.Is(err, domain.ErrInviteCodeCollision) {
			break
		}
	}
	if errors.Is(err, domain.ErrInviteCodeCollision) {
		return nil, fmt.Errorf("invite code collision retry budget exhausted: %w", err)
	}
	if err != nil {
		return nil, err
	}
	inviteURL, err := buildInviteURL(s.inviteURLBase, result.Invite.Code)
	if err != nil {
		return nil, fmt.Errorf("build invite URL: %w", err)
	}
	return &CreateInviteOutput{Invite: result.Invite, InviteURL: inviteURL, Created: result.Created}, nil
}

func buildInviteURL(base, code string) (string, error) {
	if !inviteCodePattern.MatchString(code) {
		return "", errors.New("invite code must be eight Base62 characters")
	}
	normalized, err := normalizeInviteURLBase(base)
	if err != nil {
		return "", err
	}
	joined, err := url.JoinPath(normalized, code)
	if err != nil {
		return "", fmt.Errorf("append invite URL path: %w", err)
	}
	return joined, nil
}

func normalizeInviteURLBase(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invite URL base must be an absolute HTTPS URL without user info, query, or fragment")
	}
	cleanedPath := path.Clean(parsed.Path)
	if cleanedPath == "." {
		cleanedPath = ""
	}
	parsed.Path = cleanedPath
	parsed.RawPath = ""
	return parsed.String(), nil
}

type AvailableInvite struct {
	Invite    domain.Invite
	InviteURL string
}

func (s *Service) ListAvailableInvites(ctx context.Context, groupID, callerUserID string) ([]AvailableInvite, error) {
	if strings.TrimSpace(groupID) == "" || strings.TrimSpace(callerUserID) == "" {
		return nil, domain.ErrGroupNotFound
	}
	invites, err := s.repo.ListAvailableInvites(ctx, groupID, callerUserID)
	if err != nil {
		return nil, err
	}
	items := make([]AvailableInvite, 0, len(invites))
	for _, invite := range invites {
		inviteURL, buildErr := buildInviteURL(s.inviteURLBase, invite.Code)
		if buildErr != nil {
			return nil, fmt.Errorf("build invite URL: %w", buildErr)
		}
		items = append(items, AvailableInvite{Invite: invite, InviteURL: inviteURL})
	}
	return items, nil
}

// RevokeInvite is idempotent: revoking an already revoked or unknown-state
// invite still succeeds once the caller and invite are confirmed valid.
func (s *Service) RevokeInvite(ctx context.Context, groupID, inviteID, callerUserID string) error {
	if strings.TrimSpace(groupID) == "" || strings.TrimSpace(callerUserID) == "" {
		return domain.ErrGroupNotFound
	}
	if strings.TrimSpace(inviteID) == "" {
		return domain.ErrInviteNotFound
	}
	return s.repo.RevokeInvite(ctx, groupID, inviteID, callerUserID)
}

// PreviewInvite lets an authenticated nonmember see the group name, active
// member count, and Captain display name for a valid invite code (AC-4).
func (s *Service) PreviewInvite(ctx context.Context, code string) (*domain.InvitePreview, error) {
	if !inviteCodePattern.MatchString(code) {
		return nil, domain.ErrInviteNotFound
	}
	return s.repo.PreviewInvite(ctx, code)
}

// JoinGroup redeems an invite code: a new join or reactivation validates
// the invite, enforces capacity, and activates the membership; an already
// active member gets idempotent success (AC-5).
func (s *Service) JoinGroup(ctx context.Context, code, callerUserID string) (*domain.JoinResult, error) {
	if !inviteCodePattern.MatchString(code) {
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
		return nil, domain.ErrGroupNotFound
	}
	if strings.TrimSpace(targetMembershipID) == "" {
		return nil, domain.ErrMemberNotFound
	}
	return s.repo.TransferCaptain(ctx, groupID, targetMembershipID, callerUserID)
}

func (s *Service) RenameGroup(ctx context.Context, groupID, callerUserID, rawName string) (*domain.Group, error) {
	if strings.TrimSpace(groupID) == "" || strings.TrimSpace(callerUserID) == "" {
		return nil, domain.ErrGroupNotFound
	}
	name := strings.TrimSpace(rawName)
	if length := utf8.RuneCountInString(name); length < minGroupNameLength || length > maxGroupNameLength {
		return nil, domain.ErrInvalidInput
	}
	return s.repo.RenameGroup(ctx, groupID, callerUserID, name)
}

func (s *Service) DisbandGroup(ctx context.Context, groupID, callerUserID string) error {
	if strings.TrimSpace(groupID) == "" || strings.TrimSpace(callerUserID) == "" {
		return domain.ErrGroupNotFound
	}
	return s.repo.DisbandGroup(ctx, groupID, callerUserID)
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
