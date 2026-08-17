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
}

type CreateInviteResult struct {
	Invite  *domain.Invite
	Created bool
}

type Repository interface {
	CreateGroup(context.Context, CreateGroupParams) (*domain.Group, *domain.Membership, error)
	ListGroups(context.Context, ListGroupsParams) ([]domain.GroupListItem, *string, error)
	GetGroupDetail(ctx context.Context, groupID, callerUserID string) (*domain.GroupDetail, error)
	CreateOrReuseInvite(context.Context, CreateInviteParams) (*CreateInviteResult, error)
	RevokeInvite(ctx context.Context, groupID, inviteID, callerUserID string) error
	PreviewInvite(ctx context.Context, code string) (*domain.InvitePreview, error)
	RedeemInvite(ctx context.Context, code, callerUserID string) (*domain.JoinResult, error)
	LeaveOrRemoveMember(ctx context.Context, groupID, targetMembershipID, callerUserID string) error
	TransferCaptain(ctx context.Context, groupID, targetMembershipID, callerUserID string) (*domain.CaptainTransfer, error)
	ListActivities(context.Context, ListActivitiesParams) ([]domain.Activity, *string, error)
}

type ListActivitiesParams struct {
	GroupID      string
	CallerUserID string
	Cursor       *string
	Limit        int
}
