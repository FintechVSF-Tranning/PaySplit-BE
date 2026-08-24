package repository

import (
	"context"
	"time"

	"paysplit-backend/internal/modules/group/domain"
)

type CreateGroupParams struct {
	Name      string
	Currency  string
	CreatedBy string
}

type ListGroupsParams struct {
	UserID string
	Cursor *string
	Limit  int
}

type CreateInviteParams struct {
	GroupID      string
	CallerUserID string
	Code         string
	ExpiresIn    time.Duration
	MaxUses      *int
	Regenerate   bool
	// HasConfiguration is true when any policy JSON field was present, even
	// when its decoded value was false. The repository rechecks this permission
	// after acquiring the group lock.
	HasConfiguration bool
}

type CreateInviteResult struct {
	Invite  *domain.Invite
	Created bool
}

type Repository interface {
	CreateGroup(context.Context, CreateGroupParams) (*domain.Group, *domain.Membership, error)
	ListGroups(context.Context, ListGroupsParams) ([]domain.GroupListItem, *string, error)
	GetGroupDetail(ctx context.Context, groupID, callerUserID string) (*domain.GroupDetail, error)
	GetActiveMembershipRole(ctx context.Context, groupID, callerUserID string) (string, error)
	CreateOrReuseInvite(context.Context, CreateInviteParams) (*CreateInviteResult, error)
	ListAvailableInvites(ctx context.Context, groupID, callerUserID string) ([]domain.Invite, error)
	RevokeInvite(ctx context.Context, groupID, inviteID, callerUserID string) error
	PreviewInvite(ctx context.Context, code string) (*domain.InvitePreview, error)
	RedeemInvite(ctx context.Context, code, callerUserID string) (*domain.JoinResult, error)
	LeaveOrRemoveMember(ctx context.Context, groupID, targetMembershipID, callerUserID string) error
	TransferCaptain(ctx context.Context, groupID, targetMembershipID, callerUserID string) (*domain.CaptainTransfer, error)
	RenameGroup(ctx context.Context, groupID, callerUserID, name string) (*domain.Group, error)
	DisbandGroup(ctx context.Context, groupID, callerUserID string) error
	ListActivities(context.Context, ListActivitiesParams) ([]domain.Activity, *string, error)
}

type ListActivitiesParams struct {
	GroupID      string
	CallerUserID string
	Cursor       *string
	Limit        int
}
