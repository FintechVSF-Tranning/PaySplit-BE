package http

import (
	"encoding/json"
	"strconv"
	"time"

	"paysplit-backend/internal/modules/group/domain"
)

type groupResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
	// Trạng thái khóa gửi hóa đơn một chiều của V1 (Spec 0008 Public response fields).
	BillSubmissionLocked   bool       `json:"bill_submission_locked"`
	BillSubmissionLockedAt *time.Time `json:"bill_submission_locked_at"`
}

type membershipResponse struct {
	ID       string     `json:"id"`
	GroupID  string     `json:"group_id"`
	UserID   string     `json:"user_id"`
	Role     string     `json:"role"`
	Status   string     `json:"status"`
	JoinedAt time.Time  `json:"joined_at"`
	LeftAt   *time.Time `json:"left_at"`
}

type memberResponse struct {
	MembershipID string    `json:"membership_id"`
	UserID       string    `json:"user_id"`
	DisplayName  string    `json:"display_name"`
	AvatarURL    *string   `json:"avatar_url"`
	Role         string    `json:"role"`
	JoinedAt     time.Time `json:"joined_at"`
}

type balanceResponse struct {
	MemberID   string `json:"member_id"`
	NetBalance string `json:"net_balance"`
}

type groupListItemResponse struct {
	Group              groupResponse `json:"group"`
	CallerMembershipID string        `json:"caller_membership_id"`
	CallerRole         string        `json:"caller_role"`
	ActiveMemberCount  int           `json:"active_member_count"`
}

func newGroupResponse(g domain.Group) groupResponse {
	resp := groupResponse{ID: g.ID, Name: g.Name, Currency: g.Currency, CreatedAt: g.CreatedAt}
	if g.BillSubmissionLockedAt != nil {
		resp.BillSubmissionLocked = true
		resp.BillSubmissionLockedAt = g.BillSubmissionLockedAt
	}
	return resp
}

func newMembershipResponse(m domain.Membership) membershipResponse {
	return membershipResponse{ID: m.ID, GroupID: m.GroupID, UserID: m.UserID, Role: m.Role, Status: m.Status, JoinedAt: m.JoinedAt, LeftAt: m.LeftAt}
}

func (h *Handler) newMemberResponse(m domain.Member) memberResponse {
	var avatarURL *string
	if m.AvatarObjectKey != nil {
		v := h.avatarURL(*m.AvatarObjectKey)
		avatarURL = &v
	}
	return memberResponse{MembershipID: m.MembershipID, UserID: m.UserID, DisplayName: m.DisplayName, AvatarURL: avatarURL, Role: m.Role, JoinedAt: m.JoinedAt}
}

func newBalanceResponse(b domain.Balance) balanceResponse {
	return balanceResponse{MemberID: b.MemberID, NetBalance: strconv.FormatInt(b.NetBalance, 10)}
}

func newGroupListItemResponse(item domain.GroupListItem) groupListItemResponse {
	return groupListItemResponse{
		Group:              newGroupResponse(item.Group),
		CallerMembershipID: item.CallerMembershipID,
		CallerRole:         item.CallerRole,
		ActiveMemberCount:  item.ActiveMemberCount,
	}
}

type inviteResponse struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	InviteURL string    `json:"invite_url"`
	ExpiresAt time.Time `json:"expires_at"`
	MaxUses   *int      `json:"max_uses"`
	UseCount  int       `json:"use_count"`
}

func newInviteResponse(inv domain.Invite, inviteURL string) inviteResponse {
	return inviteResponse{ID: inv.ID, Code: inv.Code, InviteURL: inviteURL, ExpiresAt: inv.ExpiresAt, MaxUses: inv.MaxUses, UseCount: inv.UseCount}
}

type invitePreviewResponse struct {
	GroupName          string `json:"group_name"`
	ActiveMemberCount  int    `json:"active_member_count"`
	CaptainDisplayName string `json:"captain_display_name"`
}

func newInvitePreviewResponse(p domain.InvitePreview) invitePreviewResponse {
	return invitePreviewResponse{GroupName: p.GroupName, ActiveMemberCount: p.ActiveMemberCount, CaptainDisplayName: p.CaptainDisplayName}
}

type joinResultResponse struct {
	GroupID      string `json:"group_id"`
	MembershipID string `json:"membership_id"`
	Role         string `json:"role"`
	Status       string `json:"status"`
	Result       string `json:"result"`
}

func newJoinResultResponse(j domain.JoinResult) joinResultResponse {
	return joinResultResponse{GroupID: j.GroupID, MembershipID: j.MembershipID, Role: j.Role, Status: j.Status, Result: j.Result}
}

type captainTransferResponse struct {
	PreviousCaptainMembershipID string `json:"previous_captain_member_id"`
	CurrentCaptainMembershipID  string `json:"current_captain_member_id"`
}

func newCaptainTransferResponse(t domain.CaptainTransfer) captainTransferResponse {
	return captainTransferResponse{PreviousCaptainMembershipID: t.PreviousCaptainMembershipID, CurrentCaptainMembershipID: t.CurrentCaptainMembershipID}
}

type activityActorResponse struct {
	MembershipID string  `json:"member_id"`
	UserID       string  `json:"user_id"`
	DisplayName  string  `json:"display_name"`
	AvatarURL    *string `json:"avatar_url"`
}

type activityResponse struct {
	ID          string                `json:"id"`
	ActionType  string                `json:"action_type"`
	Description string                `json:"description"`
	Actor       activityActorResponse `json:"actor"`
	Metadata    json.RawMessage       `json:"metadata"`
	CreatedAt   time.Time             `json:"created_at"`
}

func (h *Handler) newActivityResponse(a domain.Activity) activityResponse {
	var avatarURL *string
	if a.Actor.AvatarObjectKey != nil {
		v := h.avatarURL(*a.Actor.AvatarObjectKey)
		avatarURL = &v
	}
	return activityResponse{
		ID:          a.ID,
		ActionType:  a.ActionType,
		Description: a.Description,
		Actor: activityActorResponse{
			MembershipID: a.Actor.MembershipID,
			UserID:       a.Actor.UserID,
			DisplayName:  a.Actor.DisplayName,
			AvatarURL:    avatarURL,
		},
		Metadata:  a.Metadata,
		CreatedAt: a.CreatedAt,
	}
}
