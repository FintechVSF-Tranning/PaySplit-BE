package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/group/domain"
	"paysplit-backend/internal/modules/group/repository"
	dbgen "paysplit-backend/internal/modules/group/repository/postgres/sqlc"
	"paysplit-backend/internal/platform/database"
)

// defaultListLimit and maxListLimit bound both the group list and, later,
// the activity timeline: spec 0002 fixes 20 default, 1 through 100 accepted.
const (
	defaultListLimit = 20
	maxListLimit     = 100
	// maxGroupActiveMembers is the v1 group capacity spec 0002 fixes.
	maxGroupActiveMembers = 50
)

type postgresRepository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) repository.Repository {
	if pool == nil {
		panic("group repository pool must not be nil")
	}
	return &postgresRepository{pool: pool}
}

func (r *postgresRepository) CreateGroup(ctx context.Context, p repository.CreateGroupParams) (*domain.Group, *domain.Membership, error) {
	createdBy, err := uuid.Parse(p.CreatedBy)
	if err != nil {
		return nil, nil, domain.ErrInvalidInput
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin create group: %w", err)
	}
	defer tx.Rollback(ctx)

	q := dbgen.New(tx)
	groupRow, err := q.CreateGroup(ctx, dbgen.CreateGroupParams{Name: p.Name, Currency: p.Currency, CreatedBy: pgUUID(createdBy)})
	if err != nil {
		return nil, nil, mapWriteError(err)
	}
	memberRow, err := q.CreateInitialCaptainMembership(ctx, dbgen.CreateInitialCaptainMembershipParams{GroupID: groupRow.ID, UserID: pgUUID(createdBy)})
	if err != nil {
		return nil, nil, fmt.Errorf("create captain membership: %w", err)
	}
	displayName, err := q.GetUserDisplayName(ctx, pgUUID(createdBy))
	if err != nil {
		return nil, nil, fmt.Errorf("load creator display name: %w", err)
	}

	group := mapGroup(groupRow)
	metadata, err := json.Marshal(map[string]string{"group_id": group.ID})
	if err != nil {
		return nil, nil, fmt.Errorf("encode group_created metadata: %w", err)
	}
	description := fmt.Sprintf("%s created the group %q", displayName, group.Name)
	if _, err = tx.Exec(ctx, `INSERT INTO group_activities (group_id, actor_member_id, action_type, description, metadata) VALUES ($1,$2,'group_created',$3,$4)`, groupRow.ID, memberRow.ID, description, metadata); err != nil {
		return nil, nil, fmt.Errorf("insert group_created activity: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit create group: %w", err)
	}
	return group, mapMembership(memberRow), nil
}

func (r *postgresRepository) ListGroups(ctx context.Context, p repository.ListGroupsParams) ([]domain.GroupListItem, *string, error) {
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return nil, nil, domain.ErrInvalidInput
	}
	limit := p.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	var cursorCreatedAt pgtype.Timestamptz
	var cursorID pgtype.UUID
	if p.Cursor != nil {
		createdAt, id, decodeErr := decodeCursor(*p.Cursor)
		if decodeErr != nil {
			return nil, nil, domain.ErrInvalidCursor
		}
		cursorCreatedAt = pgtype.Timestamptz{Time: createdAt, Valid: true}
		cursorID = pgUUID(id)
	}

	// Fetch one extra row to detect whether a next page exists without a
	// separate count query.
	rows, err := dbgen.New(r.pool).ListActiveGroupsForUser(ctx, dbgen.ListActiveGroupsForUserParams{
		UserID:          pgUUID(userID),
		Limit:           int32(limit + 1),
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list groups: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]domain.GroupListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, domain.GroupListItem{
			Group: domain.Group{
				ID:        uuid.UUID(row.ID.Bytes).String(),
				Name:      row.Name,
				Currency:  row.Currency,
				CreatedBy: uuid.UUID(row.CreatedBy.Bytes).String(),
				CreatedAt: row.CreatedAt.Time,
				Status:    domain.GroupActive,
			},
			CallerMembershipID: uuid.UUID(row.MembershipID.Bytes).String(),
			CallerRole:         fmt.Sprint(row.Role),
			ActiveMemberCount:  int(row.ActiveMemberCount),
		})
	}

	var nextCursor *string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1].Group
		c := encodeCursor(last.CreatedAt, last.ID)
		nextCursor = &c
	}
	return items, nextCursor, nil
}

func (r *postgresRepository) GetGroupDetail(ctx context.Context, groupID, callerUserID string) (*domain.GroupDetail, error) {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}

	q := dbgen.New(r.pool)
	groupRow, err := q.GetGroupByID(ctx, pgUUID(gid))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get group: %w", err)
	}
	// A missing group and a nonmember caller must map to the same error so
	// group detail never reveals which one occurred (spec 0002 invariant 12).
	membershipRow, err := q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get caller membership: %w", err)
	}
	memberRows, err := q.ListActiveMembers(ctx, pgUUID(gid))
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	balanceRows, err := q.ListGroupBalances(ctx, pgUUID(gid))
	if err != nil {
		return nil, fmt.Errorf("list balances: %w", err)
	}

	members := make([]domain.Member, 0, len(memberRows))
	for _, m := range memberRows {
		member := domain.Member{
			MembershipID: uuid.UUID(m.MembershipID.Bytes).String(),
			UserID:       uuid.UUID(m.UserID.Bytes).String(),
			DisplayName:  m.DisplayName,
			Role:         fmt.Sprint(m.Role),
			JoinedAt:     m.JoinedAt.Time,
		}
		if m.AvatarObjectKey.Valid {
			v := m.AvatarObjectKey.String
			member.AvatarObjectKey = &v
		}
		members = append(members, member)
	}
	balances := make([]domain.Balance, 0, len(balanceRows))
	for _, b := range balanceRows {
		balances = append(balances, domain.Balance{MemberID: uuid.UUID(b.MemberID.Bytes).String(), NetBalance: b.NetBalance})
	}

	return &domain.GroupDetail{
		Group:      *mapGroup(groupRow),
		Members:    members,
		Balances:   balances,
		CallerRole: fmt.Sprint(membershipRow.Role),
	}, nil
}

func (r *postgresRepository) GetActiveMembershipRole(ctx context.Context, groupID, callerUserID string) (string, error) {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return "", domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return "", domain.ErrGroupNotFound
	}

	q := dbgen.New(r.pool)
	if _, err = q.GetGroupByID(ctx, pgUUID(gid)); errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrGroupNotFound
	} else if err != nil {
		return "", fmt.Errorf("get active group: %w", err)
	}
	membership, err := q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrGroupNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get active membership: %w", err)
	}
	return fmt.Sprint(membership.Role), nil
}

func (r *postgresRepository) CreateOrReuseInvite(ctx context.Context, p repository.CreateInviteParams) (*repository.CreateInviteResult, error) {
	gid, err := uuid.Parse(p.GroupID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(p.CallerUserID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create invite: %w", err)
	}
	defer tx.Rollback(ctx)

	// Locking the group row first serializes this against every other
	// group mutation, including a concurrent Captain transfer (spec 0002
	// invariant 1).
	q := dbgen.New(tx)
	if err = database.LockActiveGroup(ctx, tx, gid); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return nil, domain.ErrGroupNotFound
		}
		return nil, fmt.Errorf("lock group: %w", err)
	}

	membership, err := q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get caller membership: %w", err)
	}
	if (p.HasConfiguration || p.Regenerate) && fmt.Sprint(membership.Role) != domain.RoleCaptain {
		return nil, domain.ErrCaptainRequired
	}

	displayName, err := q.GetUserDisplayName(ctx, pgUUID(uid))
	if err != nil {
		return nil, fmt.Errorf("load captain display name: %w", err)
	}

	now := time.Now()
	if p.Regenerate {
		revoked, revokeErr := q.RevokeAvailableInvites(ctx, dbgen.RevokeAvailableInvitesParams{GroupID: pgUUID(gid), RevokedAt: pgtype.Timestamptz{Time: now, Valid: true}})
		if revokeErr != nil {
			return nil, fmt.Errorf("revoke available invites: %w", revokeErr)
		}
		for _, old := range revoked {
			if actErr := insertInviteActivity(ctx, tx, gid, membership.ID, "invite_revoked", fmt.Sprintf("%s revoked an invite", displayName), old.ID, nil); actErr != nil {
				return nil, actErr
			}
		}
		invite, createErr := r.insertNewInvite(ctx, q, tx, gid, membership.ID, displayName, p, now)
		if createErr != nil {
			return nil, createErr
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit regenerate invite: %w", err)
		}
		return &repository.CreateInviteResult{Invite: invite, Created: true}, nil
	}

	candidate, err := q.FindAvailableInvite(ctx, dbgen.FindAvailableInviteParams{GroupID: pgUUID(gid), ExpiresAt: pgtype.Timestamptz{Time: now, Valid: true}})
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit reuse invite: %w", err)
		}
		return &repository.CreateInviteResult{Invite: mapInvite(candidate), Created: false}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find available invite: %w", err)
	}

	invite, err := r.insertNewInvite(ctx, q, tx, gid, membership.ID, displayName, p, now)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create invite: %w", err)
	}
	return &repository.CreateInviteResult{Invite: invite, Created: true}, nil
}

func (r *postgresRepository) insertNewInvite(ctx context.Context, q *dbgen.Queries, tx pgx.Tx, gid uuid.UUID, captainMemberID pgtype.UUID, displayName string, p repository.CreateInviteParams, now time.Time) (*domain.Invite, error) {
	var maxUses pgtype.Int4
	if p.MaxUses != nil {
		maxUses = pgtype.Int4{Int32: int32(*p.MaxUses), Valid: true}
	}
	row, err := q.CreateInvite(ctx, dbgen.CreateInviteParams{
		GroupID:   pgUUID(gid),
		Code:      p.Code,
		CreatedBy: captainMemberID,
		ExpiresAt: pgtype.Timestamptz{Time: now.Add(p.ExpiresIn), Valid: true},
		MaxUses:   maxUses,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "group_invites_code_key" {
			return nil, domain.ErrInviteCodeCollision
		}
		return nil, fmt.Errorf("insert invite: %w", err)
	}
	if err = insertInviteActivity(ctx, tx, gid, captainMemberID, "invite_created", fmt.Sprintf("%s created an invite", displayName), row.ID, &row); err != nil {
		return nil, err
	}
	return mapInvite(row), nil
}

// insertInviteActivity writes invite_created or invite_revoked activities.
// Metadata never includes the invite code (spec 0002 invariant 10).
func insertInviteActivity(ctx context.Context, tx pgx.Tx, gid uuid.UUID, actorMemberID pgtype.UUID, actionType, description string, inviteID pgtype.UUID, created *dbgen.GroupInvite) error {
	meta := map[string]any{"invite_id": uuid.UUID(inviteID.Bytes).String()}
	if created != nil {
		meta["expires_at"] = created.ExpiresAt.Time.UTC().Format(time.RFC3339)
		if created.MaxUses.Valid {
			meta["max_uses"] = created.MaxUses.Int32
		} else {
			meta["max_uses"] = nil
		}
	}
	return insertActivity(ctx, tx, gid, actorMemberID, actionType, description, meta)
}

// insertActivity writes a single group_activities row in the caller's
// transaction, per spec 0002 invariant that every group mutation appends
// its activity atomically with the mutation itself.
func insertActivity(ctx context.Context, tx pgx.Tx, gid uuid.UUID, actorMemberID pgtype.UUID, actionType, description string, meta map[string]any) error {
	metadata, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode %s metadata: %w", actionType, err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO group_activities (group_id, actor_member_id, action_type, description, metadata) VALUES ($1,$2,$3,$4,$5)`, gid, actorMemberID, actionType, description, metadata); err != nil {
		return fmt.Errorf("insert %s activity: %w", actionType, err)
	}
	return nil
}

func (r *postgresRepository) ListAvailableInvites(ctx context.Context, groupID, callerUserID string) ([]domain.Invite, error) {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}

	q := dbgen.New(r.pool)
	if _, err = q.GetGroupByID(ctx, pgUUID(gid)); errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get active group: %w", err)
	}
	if _, err = q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)}); errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get active membership: %w", err)
	}

	rows, err := q.ListAvailableInvites(ctx, dbgen.ListAvailableInvitesParams{
		GroupID:   pgUUID(gid),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list available invites: %w", err)
	}
	invites := make([]domain.Invite, 0, len(rows))
	for _, row := range rows {
		invites = append(invites, *mapInvite(row))
	}
	return invites, nil
}

func (r *postgresRepository) RevokeInvite(ctx context.Context, groupID, inviteID, callerUserID string) error {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return domain.ErrGroupNotFound
	}
	iid, err := uuid.Parse(inviteID)
	if err != nil {
		return domain.ErrInviteNotFound
	}
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return domain.ErrGroupNotFound
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin revoke invite: %w", err)
	}
	defer tx.Rollback(ctx)

	q := dbgen.New(tx)
	if err = database.LockActiveGroup(ctx, tx, gid); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return domain.ErrGroupNotFound
		}
		return fmt.Errorf("lock group: %w", err)
	}

	membership, err := q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrGroupNotFound
	}
	if err != nil {
		return fmt.Errorf("get caller membership: %w", err)
	}
	if fmt.Sprint(membership.Role) != domain.RoleCaptain {
		return domain.ErrCaptainRequired
	}

	invite, err := q.GetInviteByIDForGroup(ctx, dbgen.GetInviteByIDForGroupParams{ID: pgUUID(iid), GroupID: pgUUID(gid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrInviteNotFound
	}
	if err != nil {
		return fmt.Errorf("get invite: %w", err)
	}
	if invite.RevokedAt.Valid {
		// Revocation is idempotent: a retry after success still returns 204.
		return tx.Commit(ctx)
	}

	now := time.Now()
	if err = q.RevokeInviteByID(ctx, dbgen.RevokeInviteByIDParams{ID: invite.ID, RevokedAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	displayName, err := q.GetUserDisplayName(ctx, pgUUID(uid))
	if err != nil {
		return fmt.Errorf("load captain display name: %w", err)
	}
	if err = insertInviteActivity(ctx, tx, gid, membership.ID, "invite_revoked", fmt.Sprintf("%s revoked an invite", displayName), invite.ID, nil); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit revoke invite: %w", err)
	}
	return nil
}

func (r *postgresRepository) PreviewInvite(ctx context.Context, code string) (*domain.InvitePreview, error) {
	q := dbgen.New(r.pool)
	invite, err := q.GetInviteByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInviteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get invite by code: %w", err)
	}
	if !inviteAvailable(invite, time.Now()) {
		return nil, domain.ErrInviteNotFound
	}

	group, err := q.GetGroupByID(ctx, invite.GroupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInviteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get invite group: %w", err)
	}
	activeCount, err := q.CountActiveMembers(ctx, invite.GroupID)
	if err != nil {
		return nil, fmt.Errorf("count active members: %w", err)
	}
	captainName, err := q.GetActiveCaptainDisplayName(ctx, invite.GroupID)
	if err != nil {
		return nil, fmt.Errorf("get active captain display name: %w", err)
	}

	return &domain.InvitePreview{GroupName: group.Name, ActiveMemberCount: int(activeCount), CaptainDisplayName: captainName}, nil
}

func (r *postgresRepository) RedeemInvite(ctx context.Context, code, callerUserID string) (*domain.JoinResult, error) {
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return nil, domain.ErrInviteNotFound
	}

	// Resolve the group outside the transaction so an unknown code fails
	// fast without opening one.
	groupID, err := dbgen.New(r.pool).GetInviteGroupIDByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInviteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve invite group: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin redeem invite: %w", err)
	}
	defer tx.Rollback(ctx)

	// Locking the group row first serializes concurrent redemptions and
	// every other group mutation (spec 0002 invariant 1).
	q := dbgen.New(tx)
	if err = database.LockActiveGroup(ctx, tx, groupID); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return nil, domain.ErrInviteNotFound
		}
		return nil, fmt.Errorf("lock group: %w", err)
	}

	existing, err := q.GetMembershipByUserForUpdate(ctx, dbgen.GetMembershipByUserForUpdateParams{GroupID: groupID, UserID: pgUUID(uid)})
	hasExisting := true
	if errors.Is(err, pgx.ErrNoRows) {
		hasExisting = false
	} else if err != nil {
		return nil, fmt.Errorf("get existing membership: %w", err)
	}

	// An already active member gets idempotent success before any invite
	// availability or capacity check, usage increment, or activity (spec
	// 0002 invariant 11).
	if hasExisting && fmt.Sprint(existing.Status) == domain.MembershipActive {
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit already active join: %w", err)
		}
		return &domain.JoinResult{
			GroupID:      uuid.UUID(groupID.Bytes).String(),
			MembershipID: uuid.UUID(existing.ID.Bytes).String(),
			Role:         fmt.Sprint(existing.Role),
			Status:       fmt.Sprint(existing.Status),
			Result:       domain.JoinResultAlreadyActive,
		}, nil
	}

	invite, err := q.GetInviteByCodeForUpdate(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInviteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock invite: %w", err)
	}
	now := time.Now()
	if !inviteAvailable(invite, now) {
		return nil, domain.ErrInviteNotFound
	}

	activeCount, err := q.CountActiveMembers(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("count active members: %w", err)
	}
	if activeCount >= maxGroupActiveMembers {
		return nil, domain.ErrGroupMemberLimitReached
	}

	if err = q.IncrementInviteUse(ctx, invite.ID); err != nil {
		return nil, fmt.Errorf("increment invite use: %w", err)
	}

	var membership dbgen.GroupMember
	result := domain.JoinResultJoined
	if hasExisting {
		membership, err = q.ReactivateMembership(ctx, dbgen.ReactivateMembershipParams{ID: existing.ID, JoinedAt: pgtype.Timestamptz{Time: now, Valid: true}})
		result = domain.JoinResultReactivated
	} else {
		membership, err = q.InsertMembership(ctx, dbgen.InsertMembershipParams{GroupID: groupID, UserID: pgUUID(uid)})
	}
	if err != nil {
		return nil, fmt.Errorf("upsert membership: %w", err)
	}

	actionType, verb := "member_joined", "joined"
	if result == domain.JoinResultReactivated {
		actionType, verb = "member_reactivated", "rejoined"
	}
	displayName, err := q.GetUserDisplayName(ctx, pgUUID(uid))
	if err != nil {
		return nil, fmt.Errorf("load member display name: %w", err)
	}
	if err = insertActivity(ctx, tx, uuid.UUID(groupID.Bytes), membership.ID, actionType, fmt.Sprintf("%s %s the group", displayName, verb), map[string]any{"member_id": uuid.UUID(membership.ID.Bytes).String()}); err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit redeem invite: %w", err)
	}
	return &domain.JoinResult{
		GroupID:      uuid.UUID(groupID.Bytes).String(),
		MembershipID: uuid.UUID(membership.ID.Bytes).String(),
		Role:         fmt.Sprint(membership.Role),
		Status:       fmt.Sprint(membership.Status),
		Result:       result,
	}, nil
}

func (r *postgresRepository) LeaveOrRemoveMember(ctx context.Context, groupID, targetMembershipID, callerUserID string) error {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return domain.ErrForbidden
	}
	tid, err := uuid.Parse(targetMembershipID)
	if err != nil {
		return domain.ErrForbidden
	}
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return domain.ErrForbidden
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin leave or remove member: %w", err)
	}
	defer tx.Rollback(ctx)

	// Locking the group row first serializes this against every other
	// group mutation, including invite redemption and Captain transfer
	// (spec 0002 invariant 1).
	q := dbgen.New(tx)
	if err = database.LockActiveGroup(ctx, tx, gid); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return domain.ErrGroupNotFound
		}
		return fmt.Errorf("lock group: %w", err)
	}

	target, err := q.LockMembership(ctx, dbgen.LockMembershipParams{ID: pgUUID(tid), GroupID: pgUUID(gid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("lock target membership: %w", err)
	}

	// Resolve and prove the caller is authorized (the membership owner, or
	// the active Captain acting on someone else) before any target-state or
	// target-role dependent response is produced. Checking authorization
	// first stops an unrelated caller from using the state/role of the two
	// early returns below as an oracle on a membership they don't own and
	// aren't Captain of.
	var actor dbgen.GroupMember
	if uuid.UUID(target.UserID.Bytes) == uid {
		actor = target
	} else {
		caller, callerErr := q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)})
		if errors.Is(callerErr, pgx.ErrNoRows) {
			return domain.ErrForbidden
		}
		if callerErr != nil {
			return fmt.Errorf("get caller membership: %w", callerErr)
		}
		if fmt.Sprint(caller.Role) != domain.RoleCaptain {
			return domain.ErrForbidden
		}
		actor = caller
	}

	if fmt.Sprint(target.Status) == domain.MembershipInactive {
		// Leave/remove is idempotent: an authorized retry after success
		// still returns success without regaining access.
		return tx.Commit(ctx)
	}
	// An active Captain can never leave or be removed, whether they target
	// their own membership or a Captain tries to act on someone else's
	// (unreachable in practice, since only one active Captain exists and
	// it would be the caller) (spec 0002 AC-7).
	if fmt.Sprint(target.Role) == domain.RoleCaptain {
		return domain.ErrCaptainTransferRequired
	}

	payable, err := q.SumOpenDebtorTotal(ctx, dbgen.SumOpenDebtorTotalParams{GroupID: pgUUID(gid), DebtorMemberID: target.ID})
	if err != nil {
		return fmt.Errorf("sum open payable: %w", err)
	}
	receivable, err := q.SumOpenCreditorTotal(ctx, dbgen.SumOpenCreditorTotalParams{GroupID: pgUUID(gid), CreditorMemberID: target.ID})
	if err != nil {
		return fmt.Errorf("sum open receivable: %w", err)
	}
	if payable > 0 || receivable > 0 {
		return &domain.OpenDebtsError{PayableAmount: payable, ReceivableAmount: receivable}
	}

	now := time.Now()
	if err = q.MarkMembershipInactive(ctx, dbgen.MarkMembershipInactiveParams{ID: target.ID, LeftAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		return fmt.Errorf("mark membership inactive: %w", err)
	}

	actorName, err := q.GetUserDisplayName(ctx, actor.UserID)
	if err != nil {
		return fmt.Errorf("load actor display name: %w", err)
	}
	actionType, description, meta := "member_removed", fmt.Sprintf("%s removed a member", actorName), map[string]any{"target_member_id": uuid.UUID(target.ID.Bytes).String()}
	if actor.ID == target.ID {
		actionType, description, meta = "member_left", fmt.Sprintf("%s left the group", actorName), map[string]any{"member_id": uuid.UUID(target.ID.Bytes).String()}
	} else {
		targetName, targetErr := q.GetUserDisplayName(ctx, target.UserID)
		if targetErr != nil {
			return fmt.Errorf("load target display name: %w", targetErr)
		}
		description = fmt.Sprintf("%s removed %s from the group", actorName, targetName)
	}
	if err = insertActivity(ctx, tx, gid, actor.ID, actionType, description, meta); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit leave or remove member: %w", err)
	}
	return nil
}

func (r *postgresRepository) TransferCaptain(ctx context.Context, groupID, targetMembershipID, callerUserID string) (*domain.CaptainTransfer, error) {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}
	tid, err := uuid.Parse(targetMembershipID)
	if err != nil {
		return nil, domain.ErrMemberNotFound
	}
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transfer captain: %w", err)
	}
	defer tx.Rollback(ctx)

	// A blocked transfer must fail fast rather than queue behind another
	// in-flight mutation (spec 0002 transaction contract).
	if err = database.LockActiveGroupNowait(ctx, tx, gid); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return nil, domain.ErrCaptainTransferConflict
		}
		if errors.Is(err, database.ErrGroupNotActive) {
			return nil, domain.ErrGroupNotFound
		}
		return nil, fmt.Errorf("lock group: %w", err)
	}

	q := dbgen.New(tx)
	caller, err := q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get caller membership: %w", err)
	}
	if fmt.Sprint(caller.Role) != domain.RoleCaptain {
		return nil, domain.ErrCaptainRequired
	}
	if uuid.UUID(caller.ID.Bytes) == tid {
		return nil, domain.ErrInvalidInput
	}

	// Lock both memberships in ascending ID order so a concurrent transfer
	// in the opposite direction cannot deadlock (spec 0002 invariant 4).
	callerID, targetID := uuid.UUID(caller.ID.Bytes), tid
	var target dbgen.GroupMember
	if bytesLess(callerID, targetID) {
		if _, lockErr := q.LockMembership(ctx, dbgen.LockMembershipParams{ID: caller.ID, GroupID: pgUUID(gid)}); lockErr != nil {
			return nil, fmt.Errorf("lock caller membership: %w", lockErr)
		}
		target, err = q.LockMembership(ctx, dbgen.LockMembershipParams{ID: pgUUID(targetID), GroupID: pgUUID(gid)})
	} else {
		target, err = q.LockMembership(ctx, dbgen.LockMembershipParams{ID: pgUUID(targetID), GroupID: pgUUID(gid)})
		if err == nil {
			if _, lockErr := q.LockMembership(ctx, dbgen.LockMembershipParams{ID: caller.ID, GroupID: pgUUID(gid)}); lockErr != nil {
				return nil, fmt.Errorf("lock caller membership: %w", lockErr)
			}
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrMemberNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock target membership: %w", err)
	}
	if fmt.Sprint(target.Status) != domain.MembershipActive {
		return nil, domain.ErrMemberNotFound
	}

	if err = q.DemoteToMember(ctx, caller.ID); err != nil {
		return nil, fmt.Errorf("demote old captain: %w", err)
	}
	if err = q.PromoteToCaptain(ctx, target.ID); err != nil {
		return nil, fmt.Errorf("promote new captain: %w", err)
	}

	previousID, currentID := uuid.UUID(caller.ID.Bytes).String(), uuid.UUID(target.ID.Bytes).String()
	displayName, err := q.GetUserDisplayName(ctx, caller.UserID)
	if err != nil {
		return nil, fmt.Errorf("load previous captain display name: %w", err)
	}
	meta := map[string]any{"previous_captain_member_id": previousID, "current_captain_member_id": currentID}
	if err = insertActivity(ctx, tx, gid, caller.ID, "captain_transferred", fmt.Sprintf("%s transferred the Captain role", displayName), meta); err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transfer captain: %w", err)
	}
	return &domain.CaptainTransfer{PreviousCaptainMembershipID: previousID, CurrentCaptainMembershipID: currentID}, nil
}

func (r *postgresRepository) RenameGroup(ctx context.Context, groupID, callerUserID, name string) (*domain.Group, error) {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin rename group: %w", err)
	}
	defer tx.Rollback(ctx)

	if err = database.LockActiveGroup(ctx, tx, gid); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return nil, domain.ErrGroupNotFound
		}
		return nil, fmt.Errorf("lock group: %w", err)
	}
	q := dbgen.New(tx)
	caller, err := q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get caller membership: %w", err)
	}
	if fmt.Sprint(caller.Role) != domain.RoleCaptain {
		return nil, domain.ErrCaptainRequired
	}

	current, err := q.GetGroupByID(ctx, pgUUID(gid))
	if err != nil {
		return nil, fmt.Errorf("get current group: %w", err)
	}
	actorName, err := q.GetUserDisplayName(ctx, pgUUID(uid))
	if err != nil {
		return nil, fmt.Errorf("load captain display name: %w", err)
	}
	updated, err := q.RenameGroup(ctx, dbgen.RenameGroupParams{ID: pgUUID(gid), Name: name})
	if err != nil {
		return nil, mapWriteError(err)
	}
	if err = insertActivity(ctx, tx, gid, caller.ID, "group_renamed", fmt.Sprintf("%s đã đổi tên nhóm thành %q", actorName, name), map[string]any{
		"old_name": current.Name,
		"new_name": name,
	}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit rename group: %w", err)
	}
	return mapGroup(updated), nil
}

func (r *postgresRepository) DisbandGroup(ctx context.Context, groupID, callerUserID string) error {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return domain.ErrGroupNotFound
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin disband group: %w", err)
	}
	defer tx.Rollback(ctx)

	if err = database.LockActiveGroup(ctx, tx, gid); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return domain.ErrGroupNotFound
		}
		return fmt.Errorf("lock group: %w", err)
	}
	q := dbgen.New(tx)
	caller, err := q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrGroupNotFound
	}
	if err != nil {
		return fmt.Errorf("get caller membership: %w", err)
	}
	if fmt.Sprint(caller.Role) != domain.RoleCaptain {
		return domain.ErrCaptainRequired
	}

	unfinishedBills, err := q.CountUnfinishedBills(ctx, pgUUID(gid))
	if err != nil {
		return fmt.Errorf("count unfinished bills: %w", err)
	}
	openDebts, err := q.CountOpenDebts(ctx, pgUUID(gid))
	if err != nil {
		return fmt.Errorf("count open debts: %w", err)
	}
	if unfinishedBills > 0 || openDebts > 0 {
		return &domain.UnsettledObligationsError{
			DraftOrReviewedBillCount: unfinishedBills,
			OpenDebtCount:            openDebts,
		}
	}

	actorName, err := q.GetUserDisplayName(ctx, pgUUID(uid))
	if err != nil {
		return fmt.Errorf("load captain display name: %w", err)
	}
	now := time.Now().UTC()
	memberTag, err := tx.Exec(ctx, `UPDATE group_members SET status='inactive',left_at=$2 WHERE group_id=$1 AND status='active'`, gid, now)
	if err != nil {
		return fmt.Errorf("deactivate group members: %w", err)
	}
	if _, err = q.RevokeAvailableInvites(ctx, dbgen.RevokeAvailableInvitesParams{
		GroupID:   pgUUID(gid),
		RevokedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		return fmt.Errorf("revoke group invites: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE groups SET status='archived' WHERE id=$1 AND status='active'`, gid); err != nil {
		return fmt.Errorf("archive group: %w", err)
	}
	if err = insertActivity(ctx, tx, gid, caller.ID, "group_archived", fmt.Sprintf("%s archived the group", actorName), map[string]any{
		"member_count_deactivated": memberTag.RowsAffected(),
	}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit disband group: %w", err)
	}
	return nil
}

func (r *postgresRepository) ListActivities(ctx context.Context, p repository.ListActivitiesParams) ([]domain.Activity, *string, error) {
	gid, err := uuid.Parse(p.GroupID)
	if err != nil {
		return nil, nil, domain.ErrForbidden
	}
	uid, err := uuid.Parse(p.CallerUserID)
	if err != nil {
		return nil, nil, domain.ErrForbidden
	}

	q := dbgen.New(r.pool)
	if _, err = q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)}); errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, domain.ErrForbidden
	} else if err != nil {
		return nil, nil, fmt.Errorf("get caller membership: %w", err)
	}

	limit := p.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	var cursorCreatedAt pgtype.Timestamptz
	var cursorID pgtype.UUID
	if p.Cursor != nil {
		createdAt, id, decodeErr := decodeCursor(*p.Cursor)
		if decodeErr != nil {
			return nil, nil, domain.ErrInvalidCursor
		}
		cursorCreatedAt = pgtype.Timestamptz{Time: createdAt, Valid: true}
		cursorID = pgUUID(id)
	}

	// Fetch one extra row to detect a next page without a separate count.
	rows, err := q.ListGroupActivities(ctx, dbgen.ListGroupActivitiesParams{
		GroupID:         pgUUID(gid),
		Limit:           int32(limit + 1),
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list activities: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	activities := make([]domain.Activity, 0, len(rows))
	for _, row := range rows {
		actor := domain.ActivityActor{
			MembershipID: uuid.UUID(row.ActorMembershipID.Bytes).String(),
			UserID:       uuid.UUID(row.UserID.Bytes).String(),
			DisplayName:  row.DisplayName,
		}
		if row.AvatarObjectKey.Valid {
			v := row.AvatarObjectKey.String
			actor.AvatarObjectKey = &v
		}
		activities = append(activities, domain.Activity{
			ID:          uuid.UUID(row.ID.Bytes).String(),
			ActionType:  fmt.Sprint(row.ActionType),
			Description: row.Description,
			Actor:       actor,
			Metadata:    row.Metadata,
			CreatedAt:   row.CreatedAt.Time,
		})
	}

	var nextCursor *string
	if hasMore && len(activities) > 0 {
		last := activities[len(activities)-1]
		c := encodeCursor(last.CreatedAt, last.ID)
		nextCursor = &c
	}
	return activities, nextCursor, nil
}

// bytesLess orders two UUIDs for consistent ascending-ID lock acquisition.
func bytesLess(a, b uuid.UUID) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func inviteAvailable(i dbgen.GroupInvite, now time.Time) bool {
	if i.RevokedAt.Valid {
		return false
	}
	if !now.Before(i.ExpiresAt.Time) {
		return false
	}
	if i.MaxUses.Valid && i.UseCount >= i.MaxUses.Int32 {
		return false
	}
	return true
}

func mapInvite(i dbgen.GroupInvite) *domain.Invite {
	out := &domain.Invite{
		ID:        uuid.UUID(i.ID.Bytes).String(),
		GroupID:   uuid.UUID(i.GroupID.Bytes).String(),
		Code:      i.Code,
		CreatedBy: uuid.UUID(i.CreatedBy.Bytes).String(),
		ExpiresAt: i.ExpiresAt.Time,
		UseCount:  int(i.UseCount),
		CreatedAt: i.CreatedAt.Time,
	}
	if i.MaxUses.Valid {
		v := int(i.MaxUses.Int32)
		out.MaxUses = &v
	}
	if i.RevokedAt.Valid {
		v := i.RevokedAt.Time
		out.RevokedAt = &v
	}
	return out
}

func mapGroup(g dbgen.Group) *domain.Group {
	return &domain.Group{
		ID:        uuid.UUID(g.ID.Bytes).String(),
		Name:      g.Name,
		Currency:  g.Currency,
		CreatedBy: uuid.UUID(g.CreatedBy.Bytes).String(),
		CreatedAt: g.CreatedAt.Time,
		Status:    fmt.Sprint(g.Status),
	}
}

func mapMembership(m dbgen.GroupMember) *domain.Membership {
	out := &domain.Membership{
		ID:       uuid.UUID(m.ID.Bytes).String(),
		GroupID:  uuid.UUID(m.GroupID.Bytes).String(),
		UserID:   uuid.UUID(m.UserID.Bytes).String(),
		Role:     fmt.Sprint(m.Role),
		Status:   fmt.Sprint(m.Status),
		JoinedAt: m.JoinedAt.Time,
	}
	if m.LeftAt.Valid {
		v := m.LeftAt.Time
		out.LeftAt = &v
	}
	return out
}

func pgUUID(v uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: v, Valid: true} }

func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "groups_name_check", "groups_currency_check":
			return domain.ErrInvalidInput
		}
	}
	return fmt.Errorf("write group data: %w", err)
}

// encodeCursor and decodeCursor implement the opaque (created_at, id) cursor
// spec 0002 requires for the group list and, later, the activity timeline.
func encodeCursor(t time.Time, id string) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.UUID{}, err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.UUID{}, errors.New("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.UUID{}, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.UUID{}, err
	}
	return t, id, nil
}
