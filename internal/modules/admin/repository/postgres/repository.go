package postgres

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/admin/domain"
	"paysplit-backend/internal/modules/admin/repository"
	dbgen "paysplit-backend/internal/modules/admin/repository/postgres/sqlc"
)

var processStartTime = time.Now()

type postgresRepository struct {
	pool *pgxpool.Pool
}

// New khởi tạo adapter PostgreSQL cho module Admin.
func New(pool *pgxpool.Pool) repository.Repository {
	if pool == nil {
		panic("admin repository pool must not be nil")
	}
	return &postgresRepository{pool: pool}
}

func (r *postgresRepository) ListAccounts(ctx context.Context, filter repository.ListAccountsFilter) ([]domain.AccountSummary, int64, error) {
	q := dbgen.New(r.pool)

	var searchParam pgtype.Text
	if s := strings.TrimSpace(filter.Search); s != "" {
		searchParam = pgtype.Text{String: s, Valid: true}
	}

	var statusParam pgtype.Text
	if filter.Status != nil && strings.TrimSpace(*filter.Status) != "" {
		statusParam = pgtype.Text{String: *filter.Status, Valid: true}
	}

	var roleParam pgtype.Text
	if filter.Role != nil && strings.TrimSpace(*filter.Role) != "" {
		roleParam = pgtype.Text{String: *filter.Role, Valid: true}
	}

	total, err := q.CountAccounts(ctx, dbgen.CountAccountsParams{
		Search: searchParam,
		Status: statusParam,
		Role:   roleParam,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count accounts: %w", err)
	}

	rows, err := q.ListAccounts(ctx, dbgen.ListAccountsParams{
		Search:     searchParam,
		Status:     statusParam,
		Role:       roleParam,
		SortBy:     filter.SortBy,
		SortOrder:  filter.SortOrder,
		PageLimit:  int32(filter.Limit),
		PageOffset: int32(filter.Offset),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list accounts: %w", err)
	}

	items := make([]domain.AccountSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapAccountRow(row))
	}

	return items, total, nil
}

func (r *postgresRepository) GetAccountDetail(ctx context.Context, userID string) (*domain.AccountDetail, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, domain.ErrInvalidInput
	}
	pgUID := toPgUUID(uid)

	q := dbgen.New(r.pool)
	userRow, err := q.GetAccountByID(ctx, pgUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, fmt.Errorf("get account by id: %w", err)
	}

	activeSessions, err := q.CountActiveSessionsByUserID(ctx, pgUID)
	if err != nil {
		return nil, fmt.Errorf("count active sessions: %w", err)
	}

	groupRows, err := q.ListGroupMembershipsByUserID(ctx, pgUID)
	if err != nil {
		return nil, fmt.Errorf("list group memberships: %w", err)
	}
	groups := make([]domain.GroupMembershipSummary, 0, len(groupRows))
	for _, g := range groupRows {
		groups = append(groups, domain.GroupMembershipSummary{
			GroupID:   fromPgUUID(g.GroupID),
			GroupName: g.GroupName,
			Role:      fmt.Sprint(g.MemberRole),
			Status:    fmt.Sprint(g.MemberStatus),
			JoinedAt:  g.JoinedAt.Time,
		})
	}

	debtsRow, err := q.GetOutstandingDebtsByUserID(ctx, pgUID)
	if err != nil {
		return nil, fmt.Errorf("get outstanding debts: %w", err)
	}

	creditsRow, err := q.GetOutstandingCreditsByUserID(ctx, pgUID)
	if err != nil {
		return nil, fmt.Errorf("get outstanding credits: %w", err)
	}

	auditRows, err := q.ListRecentAuditLogsByTargetUserID(ctx, pgUID)
	if err != nil {
		return nil, fmt.Errorf("list recent audit logs: %w", err)
	}
	auditLogs := make([]domain.AuditLogEntry, 0, len(auditRows))
	for _, a := range auditRows {
		auditLogs = append(auditLogs, domain.AuditLogEntry{
			ID:           fromPgUUID(a.ID),
			AdminID:      fromPgUUID(a.AdminID),
			AdminEmail:   a.AdminEmail,
			TargetUserID: fromPgUUID(a.TargetUserID),
			Action:       fmt.Sprint(a.Action),
			Reason:       a.Reason,
			CreatedAt:    a.CreatedAt.Time,
		})
	}

	var avatarKey *string
	if userRow.AvatarObjectKey.Valid {
		avatarKey = &userRow.AvatarObjectKey.String
	}

	var emailVerifiedAt *time.Time
	if userRow.EmailVerifiedAt.Valid {
		t := userRow.EmailVerifiedAt.Time
		emailVerifiedAt = &t
	}

	var loginBlockedUntil *time.Time
	if userRow.LoginBlockedUntil.Valid {
		t := userRow.LoginBlockedUntil.Time
		loginBlockedUntil = &t
	}

	var bankCode *string
	if userRow.DefaultBankCode.Valid {
		bankCode = &userRow.DefaultBankCode.String
	}

	var bankHolder *string
	if userRow.DefaultBankAccountHolder.Valid {
		bankHolder = &userRow.DefaultBankAccountHolder.String
	}

	var maskedAccountNumber *string
	if userRow.DefaultBankAccountNumber.Valid {
		masked := MaskBankAccount(userRow.DefaultBankAccountNumber.String)
		maskedAccountNumber = &masked
	}

	detail := &domain.AccountDetail{
		Summary: domain.AccountSummary{
			ID:              fromPgUUID(userRow.ID),
			Email:           userRow.Email,
			DisplayName:     userRow.DisplayName,
			AvatarObjectKey: avatarKey,
			PhoneNumber:     userRow.PhoneNumber,
			Role:            fmt.Sprint(userRow.Role),
			Status:          fmt.Sprint(userRow.Status),
			EmailVerifiedAt: emailVerifiedAt,
			CreatedAt:       userRow.CreatedAt.Time,
			UpdatedAt:       userRow.UpdatedAt.Time,
		},
		FailedLoginCount:  int(userRow.FailedLoginCount),
		LoginBlockedUntil: loginBlockedUntil,
		Bank: domain.BankSnapshot{
			BankCode:          bankCode,
			BankAccountNumber: maskedAccountNumber,
			BankAccountHolder: bankHolder,
		},
		ActiveSessionsCount: activeSessions,
		Groups:              groups,
		Financials: domain.FinancialSummary{
			OutstandingDebtsCount:   debtsRow.OutstandingDebtsCount,
			TotalDebtAmountVND:      debtsRow.TotalDebtAmountVnd,
			OutstandingCreditsCount: creditsRow.OutstandingCreditsCount,
			TotalCreditAmountVND:    creditsRow.TotalCreditAmountVnd,
		},
		RecentAuditLogs: auditLogs,
	}

	return detail, nil
}

func (r *postgresRepository) UpdateAccountStatusWithRevocation(ctx context.Context, input repository.UpdateStatusInput) (*domain.SafeUser, *domain.WarningMeta, error) {
	targetUID, err := uuid.Parse(input.TargetUserID)
	if err != nil {
		return nil, nil, domain.ErrInvalidInput
	}
	adminUID, err := uuid.Parse(input.AdminID)
	if err != nil {
		return nil, nil, domain.ErrInvalidInput
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	q := dbgen.New(tx)
	targetUser, err := q.GetAccountByID(ctx, toPgUUID(targetUID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, domain.ErrAccountNotFound
		}
		return nil, nil, fmt.Errorf("get target account: %w", err)
	}

	// Tự bảo vệ: admin không được tự thay đổi trạng thái của chính mình
	if targetUID == adminUID {
		return nil, nil, domain.ErrCannotModifySelf
	}

	targetRole := fmt.Sprint(targetUser.Role)
	targetStatus := fmt.Sprint(targetUser.Status)

	// Bảo vệ quyền admin: không được suspend/lock admin khác
	if targetRole == "admin" && (input.NewStatus == "suspended" || input.NewStatus == "locked") {
		return nil, nil, domain.ErrCannotModifyAdmin
	}

	// Không cho phép chuyển đổi từ pending_verification sang active/suspended/locked qua API status
	if targetStatus == "pending_verification" {
		return nil, nil, domain.ErrInvalidStatusTransition
	}

	updatedUserRow, err := q.UpdateUserStatus(ctx, dbgen.UpdateUserStatusParams{
		ID:     toPgUUID(targetUID),
		Status: input.NewStatus,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("update user status: %w", err)
	}

	// Nếu chuyển sang suspended hoặc locked thì thu hồi toàn bộ session và refresh token
	if input.NewStatus == "suspended" || input.NewStatus == "locked" {
		revokedReason := pgtype.Text{String: "admin_" + input.NewStatus, Valid: true}
		if err := q.RevokeSessionsByUserID(ctx, dbgen.RevokeSessionsByUserIDParams{
			UserID:        toPgUUID(targetUID),
			RevokedReason: revokedReason,
		}); err != nil {
			return nil, nil, fmt.Errorf("revoke sessions: %w", err)
		}

		if err := q.RevokeRefreshTokensByUserID(ctx, toPgUUID(targetUID)); err != nil {
			return nil, nil, fmt.Errorf("revoke refresh tokens: %w", err)
		}
	}

	// Ghi log kiểm toán admin_audit_logs
	if _, err := q.CreateAdminAuditLog(ctx, dbgen.CreateAdminAuditLogParams{
		AdminID:      toPgUUID(adminUID),
		TargetUserID: toPgUUID(targetUID),
		Action:       input.NewStatus,
		Reason:       input.Reason,
	}); err != nil {
		return nil, nil, fmt.Errorf("create admin audit log: %w", err)
	}

	// Kiểm tra nghĩa vụ tài chính chưa tất toán để gửi cảnh báo
	debtsRow, err := q.GetOutstandingDebtsByUserID(ctx, toPgUUID(targetUID))
	if err != nil {
		return nil, nil, fmt.Errorf("get outstanding debts: %w", err)
	}

	creditsRow, err := q.GetOutstandingCreditsByUserID(ctx, toPgUUID(targetUID))
	if err != nil {
		return nil, nil, fmt.Errorf("get outstanding credits: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit status update: %w", err)
	}

	var avatarKey *string
	if updatedUserRow.AvatarObjectKey.Valid {
		avatarKey = &updatedUserRow.AvatarObjectKey.String
	}

	var emailVerifiedAt *time.Time
	if updatedUserRow.EmailVerifiedAt.Valid {
		t := updatedUserRow.EmailVerifiedAt.Time
		emailVerifiedAt = &t
	}

	safeUser := &domain.SafeUser{
		ID:              fromPgUUID(updatedUserRow.ID),
		Email:           updatedUserRow.Email,
		DisplayName:     updatedUserRow.DisplayName,
		AvatarObjectKey: avatarKey,
		PhoneNumber:     updatedUserRow.PhoneNumber,
		Role:            fmt.Sprint(updatedUserRow.Role),
		Status:          fmt.Sprint(updatedUserRow.Status),
		EmailVerifiedAt: emailVerifiedAt,
		CreatedAt:       updatedUserRow.CreatedAt.Time,
		UpdatedAt:       updatedUserRow.UpdatedAt.Time,
	}

	warning := &domain.WarningMeta{
		UnsettledDebtsCount:   debtsRow.OutstandingDebtsCount,
		UnsettledCreditsCount: creditsRow.OutstandingCreditsCount,
	}

	return safeUser, warning, nil
}

func (r *postgresRepository) GetSystemOverview(ctx context.Context) (*domain.SystemOverview, error) {
	q := dbgen.New(r.pool)

	usersRow, err := q.GetSystemUsersOverview(ctx)
	if err != nil {
		return nil, fmt.Errorf("get users overview: %w", err)
	}

	groupsTotal, err := q.GetSystemGroupsOverview(ctx)
	if err != nil {
		return nil, fmt.Errorf("get groups overview: %w", err)
	}

	billsRow, err := q.GetSystemBillsOverview(ctx)
	if err != nil {
		return nil, fmt.Errorf("get bills overview: %w", err)
	}

	debtsRow, err := q.GetSystemDebtsOverview(ctx)
	if err != nil {
		return nil, fmt.Errorf("get debts overview: %w", err)
	}

	cleanupPending, err := q.GetSystemMediaCleanupOverview(ctx)
	if err != nil {
		return nil, fmt.Errorf("get media cleanup overview: %w", err)
	}

	ocrRow, err := q.GetSystemOCRJobsOverview(ctx)
	if err != nil {
		return nil, fmt.Errorf("get ocr jobs overview: %w", err)
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	overview := &domain.SystemOverview{
		Users: domain.UsersOverview{
			Total:               usersRow.TotalUsers,
			Active:              usersRow.ActiveUsers,
			PendingVerification: usersRow.PendingVerificationUsers,
			Suspended:           usersRow.SuspendedUsers,
			Locked:              usersRow.LockedUsers,
		},
		Groups: domain.GroupsOverview{
			Total: groupsTotal,
		},
		Bills: domain.BillsOverview{
			TotalFinalized: billsRow.FinalizedBills,
			TotalDraft:     billsRow.DraftBills,
		},
		Debts: domain.DebtsOverview{
			Awaiting:            debtsRow.AwaitingDebts,
			PendingConfirmation: debtsRow.PendingConfirmationDebts,
			StalledConfirmation: debtsRow.StalledConfirmationDebts,
			Rejected:            debtsRow.RejectedDebts,
			Settled:             debtsRow.SettledDebts,
		},
		MediaCleanup: domain.MediaCleanupOverview{
			PendingJobsCount: cleanupPending,
		},
		OCRJobs: domain.OCRJobsOverview{
			Queued:     ocrRow.QueuedJobs,
			Processing: ocrRow.ProcessingJobs,
			Succeeded:  ocrRow.SucceededJobs,
			Failed:     ocrRow.FailedJobs,
		},
		Runtime: domain.RuntimeOverview{
			GoroutinesCount:  runtime.NumGoroutine(),
			AllocMemoryBytes: memStats.Alloc,
			UptimeSeconds:    int64(time.Since(processStartTime).Seconds()),
		},
	}

	return overview, nil
}

// MaskBankAccount ẩn số tài khoản ngân hàng chỉ giữ lại 4 ký tự cuối.
func MaskBankAccount(accountNumber string) string {
	accountNumber = strings.TrimSpace(accountNumber)
	if len(accountNumber) <= 4 {
		return "******" + accountNumber
	}
	return "******" + accountNumber[len(accountNumber)-4:]
}

func mapAccountRow(row dbgen.ListAccountsRow) domain.AccountSummary {
	var avatarKey *string
	if row.AvatarObjectKey.Valid {
		avatarKey = &row.AvatarObjectKey.String
	}

	var emailVerifiedAt *time.Time
	if row.EmailVerifiedAt.Valid {
		t := row.EmailVerifiedAt.Time
		emailVerifiedAt = &t
	}

	return domain.AccountSummary{
		ID:              fromPgUUID(row.ID),
		Email:           row.Email,
		DisplayName:     row.DisplayName,
		AvatarObjectKey: avatarKey,
		PhoneNumber:     row.PhoneNumber,
		Role:            fmt.Sprint(row.Role),
		Status:          fmt.Sprint(row.Status),
		EmailVerifiedAt: emailVerifiedAt,
		CreatedAt:       row.CreatedAt.Time,
		UpdatedAt:       row.UpdatedAt.Time,
	}
}

func toPgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func fromPgUUID(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	parsed, err := uuid.FromBytes(u.Bytes[:])
	if err != nil {
		return ""
	}
	return parsed.String()
}
