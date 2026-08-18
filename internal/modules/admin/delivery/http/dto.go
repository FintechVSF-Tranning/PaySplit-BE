package http

import (
	"time"

	"paysplit-backend/internal/modules/admin/domain"
)

type accountSummaryResponse struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	DisplayName     string     `json:"display_name"`
	AvatarURL       string     `json:"avatar_url,omitempty"`
	PhoneNumber     string     `json:"phone_number"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type paginationResponse struct {
	Total      int64 `json:"total"`
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	TotalPages int   `json:"total_pages"`
}

type listAccountsResponse struct {
	Items      []accountSummaryResponse `json:"items"`
	Pagination paginationResponse       `json:"pagination"`
}

type bankResponse struct {
	BankCode          *string `json:"bank_code,omitempty"`
	BankAccountNumber *string `json:"bank_account_number,omitempty"`
	BankAccountHolder *string `json:"bank_account_holder,omitempty"`
}

type groupMembershipResponse struct {
	GroupID   string    `json:"group_id"`
	GroupName string    `json:"group_name"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	JoinedAt  time.Time `json:"joined_at"`
}

type financialSummaryResponse struct {
	OutstandingDebtsCount   int64 `json:"outstanding_debts_count"`
	TotalDebtAmountVND      int64 `json:"total_debt_amount_vnd"`
	OutstandingCreditsCount int64 `json:"outstanding_credits_count"`
	TotalCreditAmountVND    int64 `json:"total_credit_amount_vnd"`
}

type auditLogResponse struct {
	ID           string    `json:"id"`
	AdminID      string    `json:"admin_id"`
	AdminEmail   string    `json:"admin_email"`
	TargetUserID string    `json:"target_user_id"`
	Action       string    `json:"action"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"created_at"`
}

type accountDetailResponse struct {
	ID                  string                    `json:"id"`
	Email               string                    `json:"email"`
	DisplayName         string                    `json:"display_name"`
	AvatarURL           string                    `json:"avatar_url,omitempty"`
	PhoneNumber         string                    `json:"phone_number"`
	Role                string                    `json:"role"`
	Status              string                    `json:"status"`
	EmailVerifiedAt     *time.Time                `json:"email_verified_at,omitempty"`
	CreatedAt           time.Time                 `json:"created_at"`
	UpdatedAt           time.Time                 `json:"updated_at"`
	FailedLoginCount    int                       `json:"failed_login_count"`
	LoginBlockedUntil   *time.Time                `json:"login_blocked_until,omitempty"`
	Bank                bankResponse              `json:"bank"`
	ActiveSessionsCount int64                     `json:"active_sessions_count"`
	Groups              []groupMembershipResponse `json:"groups"`
	Financials          financialSummaryResponse  `json:"financials"`
	RecentAuditLogs     []auditLogResponse        `json:"recent_audit_logs"`
}

type updateStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type warningResponse struct {
	UnsettledDebtsCount   int64 `json:"unsettled_debts_count"`
	UnsettledCreditsCount int64 `json:"unsettled_credits_count"`
}

type updateStatusResponse struct {
	Account accountSummaryResponse `json:"account"`
	Warning warningResponse        `json:"warning"`
}

type usersOverviewResponse struct {
	Total               int64 `json:"total"`
	Active              int64 `json:"active"`
	PendingVerification int64 `json:"pending_verification"`
	Suspended           int64 `json:"suspended"`
	Locked              int64 `json:"locked"`
}

type groupsOverviewResponse struct {
	Total int64 `json:"total"`
}

type billsOverviewResponse struct {
	TotalFinalized int64 `json:"total_finalized"`
	TotalDraft     int64 `json:"total_draft"`
}

type debtsOverviewResponse struct {
	Awaiting            int64 `json:"awaiting"`
	PendingConfirmation int64 `json:"pending_confirmation"`
	StalledConfirmation int64 `json:"stalled_confirmation"`
	Rejected            int64 `json:"rejected"`
	Settled             int64 `json:"settled"`
}

type mediaCleanupOverviewResponse struct {
	PendingJobsCount int64 `json:"pending_jobs_count"`
}

type ocrJobsOverviewResponse struct {
	Queued     int64 `json:"queued"`
	Processing int64 `json:"processing"`
	Succeeded  int64 `json:"succeeded"`
	Failed     int64 `json:"failed"`
}

type runtimeOverviewResponse struct {
	GoroutinesCount  int    `json:"goroutines_count"`
	AllocMemoryBytes uint64 `json:"alloc_memory_bytes"`
	UptimeSeconds    int64  `json:"uptime_seconds"`
}

type systemOverviewResponse struct {
	Users        usersOverviewResponse        `json:"users"`
	Groups       groupsOverviewResponse       `json:"groups"`
	Bills        billsOverviewResponse        `json:"bills"`
	Debts        debtsOverviewResponse        `json:"debts"`
	MediaCleanup mediaCleanupOverviewResponse `json:"media_cleanup"`
	OCRJobs      ocrJobsOverviewResponse      `json:"ocr_jobs"`
	Runtime      runtimeOverviewResponse      `json:"runtime"`
}

func newAccountSummaryResponse(a domain.AccountSummary, avatarURL func(string) string) accountSummaryResponse {
	url := ""
	if a.AvatarObjectKey != nil && *a.AvatarObjectKey != "" {
		url = avatarURL(*a.AvatarObjectKey)
	}
	return accountSummaryResponse{
		ID:              a.ID,
		Email:           a.Email,
		DisplayName:     a.DisplayName,
		AvatarURL:       url,
		PhoneNumber:     a.PhoneNumber,
		Role:            a.Role,
		Status:          a.Status,
		EmailVerifiedAt: a.EmailVerifiedAt,
		CreatedAt:       a.CreatedAt,
		UpdatedAt:       a.UpdatedAt,
	}
}

func newAccountDetailResponse(d domain.AccountDetail, avatarURL func(string) string) accountDetailResponse {
	url := ""
	if d.Summary.AvatarObjectKey != nil && *d.Summary.AvatarObjectKey != "" {
		url = avatarURL(*d.Summary.AvatarObjectKey)
	}

	groups := make([]groupMembershipResponse, 0, len(d.Groups))
	for _, g := range d.Groups {
		groups = append(groups, groupMembershipResponse{
			GroupID:   g.GroupID,
			GroupName: g.GroupName,
			Role:      g.Role,
			Status:    g.Status,
			JoinedAt:  g.JoinedAt,
		})
	}

	auditLogs := make([]auditLogResponse, 0, len(d.RecentAuditLogs))
	for _, a := range d.RecentAuditLogs {
		auditLogs = append(auditLogs, auditLogResponse{
			ID:           a.ID,
			AdminID:      a.AdminID,
			AdminEmail:   a.AdminEmail,
			TargetUserID: a.TargetUserID,
			Action:       a.Action,
			Reason:       a.Reason,
			CreatedAt:    a.CreatedAt,
		})
	}

	return accountDetailResponse{
		ID:                  d.Summary.ID,
		Email:               d.Summary.Email,
		DisplayName:         d.Summary.DisplayName,
		AvatarURL:           url,
		PhoneNumber:         d.Summary.PhoneNumber,
		Role:                d.Summary.Role,
		Status:              d.Summary.Status,
		EmailVerifiedAt:     d.Summary.EmailVerifiedAt,
		CreatedAt:           d.Summary.CreatedAt,
		UpdatedAt:           d.Summary.UpdatedAt,
		FailedLoginCount:    d.FailedLoginCount,
		LoginBlockedUntil:   d.LoginBlockedUntil,
		Bank:                bankResponse(d.Bank),
		ActiveSessionsCount: d.ActiveSessionsCount,
		Groups:              groups,
		Financials:          financialSummaryResponse(d.Financials),
		RecentAuditLogs:     auditLogs,
	}
}

func newSystemOverviewResponse(s domain.SystemOverview) systemOverviewResponse {
	return systemOverviewResponse{
		Users: usersOverviewResponse{
			Total:               s.Users.Total,
			Active:              s.Users.Active,
			PendingVerification: s.Users.PendingVerification,
			Suspended:           s.Users.Suspended,
			Locked:              s.Users.Locked,
		},
		Groups: groupsOverviewResponse{
			Total: s.Groups.Total,
		},
		Bills: billsOverviewResponse{
			TotalFinalized: s.Bills.TotalFinalized,
			TotalDraft:     s.Bills.TotalDraft,
		},
		Debts: debtsOverviewResponse{
			Awaiting:            s.Debts.Awaiting,
			PendingConfirmation: s.Debts.PendingConfirmation,
			StalledConfirmation: s.Debts.StalledConfirmation,
			Rejected:            s.Debts.Rejected,
			Settled:             s.Debts.Settled,
		},
		MediaCleanup: mediaCleanupOverviewResponse{
			PendingJobsCount: s.MediaCleanup.PendingJobsCount,
		},
		OCRJobs: ocrJobsOverviewResponse{
			Queued:     s.OCRJobs.Queued,
			Processing: s.OCRJobs.Processing,
			Succeeded:  s.OCRJobs.Succeeded,
			Failed:     s.OCRJobs.Failed,
		},
		Runtime: runtimeOverviewResponse{
			GoroutinesCount:  s.Runtime.GoroutinesCount,
			AllocMemoryBytes: s.Runtime.AllocMemoryBytes,
			UptimeSeconds:    s.Runtime.UptimeSeconds,
		},
	}
}
