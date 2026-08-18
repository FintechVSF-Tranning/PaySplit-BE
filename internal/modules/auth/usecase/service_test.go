package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"
	"paysplit-backend/internal/modules/auth/usecase"
)

type mockPasswordManager struct {
	validateErr error
	hashErr     error
	compareErr  error
}

func (m *mockPasswordManager) Validate(p string) error {
	if m.validateErr != nil {
		return m.validateErr
	}
	if len(p) < 8 {
		return errors.New("password too short")
	}
	return nil
}
func (m *mockPasswordManager) Hash(p string) (string, error) {
	if m.hashErr != nil {
		return "", m.hashErr
	}
	return "hashed-" + p, nil
}
func (m *mockPasswordManager) Compare(hash, p string) error {
	if m.compareErr != nil {
		return m.compareErr
	}
	if hash == "hashed-"+p {
		return nil
	}
	return errors.New("mismatch")
}

type mockTokenIssuer struct{}

func (m *mockTokenIssuer) Issue(userID, role, sessionID string) (string, time.Time, error) {
	return "jwt-access-token", time.Now().Add(15 * time.Minute), nil
}

type mockMailer struct {
	verificationEmail string
	verificationOTP   string
	resetEmail        string
	resetOTP          string
}

func (m *mockMailer) SendVerification(_ context.Context, to, _, otp string, _ time.Time) error {
	m.verificationEmail = to
	m.verificationOTP = otp
	return nil
}
func (m *mockMailer) SendPasswordReset(_ context.Context, to, _, otp string, _ time.Time) error {
	m.resetEmail = to
	m.resetOTP = otp
	return nil
}

type mockBankDirectory struct{}

func (m *mockBankDirectory) Supported(code string) bool { return code == "VCB" }

type mockImageProcessor struct{}

func (m *mockImageProcessor) Convert(_ context.Context, data []byte) ([]byte, error) {
	return data, nil
}
func (m *mockImageProcessor) IsUnsupported(err error) bool { return false }

type mockAvatarStorage struct{}

func (m *mockAvatarStorage) Upload(_ context.Context, _ []byte, key string) (string, error) {
	return key, nil
}
func (m *mockAvatarStorage) Delete(context.Context, string) error { return nil }
func (m *mockAvatarStorage) URL(key string) string                { return "https://cdn.example.invalid/" + key }

type mockRepository struct {
	createUserFunc              func(ctx context.Context, p repository.CreateUserParams) (*domain.User, error)
	getByEmailFunc              func(ctx context.Context, email string) (*domain.User, error)
	getByIDFunc                 func(ctx context.Context, id string) (*domain.User, error)
	createUserTokenFunc         func(ctx context.Context, userID, kind string, hash []byte, expires time.Time) error
	verifyEmailFunc             func(ctx context.Context, email string, otpHash []byte, now time.Time) (*domain.User, error)
	resetPasswordFunc           func(ctx context.Context, email string, otpHash []byte, newHash string, now time.Time) error
	changePasswordFunc          func(ctx context.Context, userID, sessionID, current, next string, now time.Time) error
	revokeSessionFunc           func(ctx context.Context, userID, sessionID, reason string, now time.Time) error
	checkAndRecordRateLimitFunc func(ctx context.Context, action string, keys map[string][]byte, now time.Time) (time.Duration, error)
}

func (m *mockRepository) CreateUser(ctx context.Context, p repository.CreateUserParams) (*domain.User, error) {
	if m.createUserFunc != nil {
		return m.createUserFunc(ctx, p)
	}
	return &domain.User{ID: "usr-1", Email: p.Email, DisplayName: p.DisplayName, Status: domain.StatusPendingVerification}, nil
}
func (m *mockRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	if m.getByEmailFunc != nil {
		return m.getByEmailFunc(ctx, email)
	}
	return &domain.User{ID: "usr-1", Email: email, DisplayName: "User", Status: domain.StatusActive}, nil
}
func (m *mockRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return &domain.User{ID: id, Email: "user@example.com", PasswordHash: "hashed-OldPass1", Status: domain.StatusActive}, nil
}
func (m *mockRepository) CreateUserToken(ctx context.Context, userID, kind string, hash []byte, expires time.Time) error {
	if m.createUserTokenFunc != nil {
		return m.createUserTokenFunc(ctx, userID, kind, hash, expires)
	}
	return nil
}
func (m *mockRepository) VerifyEmail(ctx context.Context, email string, otpHash []byte, now time.Time) (*domain.User, error) {
	if m.verifyEmailFunc != nil {
		return m.verifyEmailFunc(ctx, email, otpHash, now)
	}
	return &domain.User{ID: "usr-1", Email: email, Status: domain.StatusActive}, nil
}
func (m *mockRepository) RecordLoginFailure(context.Context, string, time.Time) (time.Duration, error) {
	return 0, nil
}
func (m *mockRepository) CreateSession(context.Context, repository.CreateSessionParams) (*domain.User, *domain.Session, error) {
	return nil, nil, nil
}
func (m *mockRepository) RotateRefresh(context.Context, []byte, []byte, string, time.Time) (*repository.RotateRefreshResult, error) {
	return nil, nil
}
func (m *mockRepository) ValidateSession(context.Context, string, string, time.Time) (*domain.SessionIdentity, error) {
	return nil, nil
}
func (m *mockRepository) RevokeSession(ctx context.Context, userID, sessionID, reason string, now time.Time) error {
	if m.revokeSessionFunc != nil {
		return m.revokeSessionFunc(ctx, userID, sessionID, reason, now)
	}
	return nil
}
func (m *mockRepository) ResetPassword(ctx context.Context, email string, otpHash []byte, newHash string, now time.Time) error {
	if m.resetPasswordFunc != nil {
		return m.resetPasswordFunc(ctx, email, otpHash, newHash, now)
	}
	return nil
}
func (m *mockRepository) ChangePassword(ctx context.Context, userID, sessionID, newHash string, now time.Time) error {
	return nil
}
func (m *mockRepository) UpdateProfile(context.Context, string, repository.ProfilePatch) (*domain.User, error) {
	return nil, nil
}
func (m *mockRepository) SetAvatar(context.Context, string, string) (*domain.User, *string, error) {
	return nil, nil, nil
}
func (m *mockRepository) ClearAvatar(context.Context, string) (*string, error) {
	return nil, nil
}
func (m *mockRepository) CheckAndRecordRateLimit(ctx context.Context, action string, keys map[string][]byte, now time.Time) (time.Duration, error) {
	if m.checkAndRecordRateLimitFunc != nil {
		return m.checkAndRecordRateLimitFunc(ctx, action, keys, now)
	}
	return 0, nil
}
func (m *mockRepository) EnqueueMediaCleanup(context.Context, string, string) error { return nil }
func (m *mockRepository) ClaimMediaCleanup(context.Context, time.Time, int) ([]domain.MediaCleanupJob, error) {
	return nil, nil
}
func (m *mockRepository) CompleteMediaCleanup(context.Context, string, time.Time) error {
	return nil
}
func (m *mockRepository) FailMediaCleanup(context.Context, string, string, time.Time) error {
	return nil
}
func (m *mockRepository) UpdateSessionFCMToken(context.Context, string, string) error { return nil }
func (m *mockRepository) CleanupExpiredAuth(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func newTestService(repo repository.Repository, mailer usecase.Mailer, pwMgr usecase.PasswordManager) *usecase.Service {
	if pwMgr == nil {
		pwMgr = &mockPasswordManager{}
	}
	return usecase.NewService(repo, pwMgr, &mockTokenIssuer{}, mailer, &mockBankDirectory{}, &mockImageProcessor{}, &mockAvatarStorage{}, usecase.Options{
		VerificationTTL: 10 * time.Minute,
		ResetTTL:        10 * time.Minute,
		SessionTTL:      7 * 24 * time.Hour,
	})
}

// AC-1, AC-2: Sign Up with 6-digit OTP
func TestSignUp_GeneratesSixDigitOTP(t *testing.T) {
	mailer := &mockMailer{}
	var savedTokenHash []byte
	repo := &mockRepository{
		createUserFunc: func(ctx context.Context, p repository.CreateUserParams) (*domain.User, error) {
			savedTokenHash = p.VerificationTokenHash
			return &domain.User{ID: "usr-1", Email: p.Email, DisplayName: p.DisplayName, Status: domain.StatusPendingVerification}, nil
		},
	}
	svc := newTestService(repo, mailer, nil)

	out, err := svc.SignUp(context.Background(), usecase.SignUpInput{
		Email:       "test@example.com",
		PhoneNumber: "0987654321",
		DisplayName: "Test User",
		Password:    "ValidPassword1",
		ClientIP:    "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("SignUp failed: %v", err)
	}
	if !out.VerificationEmailSent {
		t.Fatal("expected verification email sent")
	}
	if len(mailer.verificationOTP) != 6 {
		t.Fatalf("expected 6-digit OTP sent to mailer, got %q", mailer.verificationOTP)
	}
	if len(savedTokenHash) != 32 {
		t.Fatalf("expected 32-byte SHA-256 token hash in repo, got %d", len(savedTokenHash))
	}
}

// AC-3: Verify Email with OTP
func TestVerifyEmail_ValidatesOTPFormat(t *testing.T) {
	repo := &mockRepository{}
	svc := newTestService(repo, &mockMailer{}, nil)

	invalidOTPs := []string{"", "12345", "1234567", "abcdef", "12345a", " 12345"}
	for _, otp := range invalidOTPs {
		_, err := svc.VerifyEmail(context.Background(), "user@example.com", otp)
		if !errors.Is(err, domain.ErrInvalidOrExpiredToken) {
			t.Fatalf("for otp %q: expected ErrInvalidOrExpiredToken, got %v", otp, err)
		}
	}

	// Valid OTP format delegates to repository
	repo.verifyEmailFunc = func(ctx context.Context, email string, otpHash []byte, now time.Time) (*domain.User, error) {
		return &domain.User{ID: "usr-1", Email: email, Status: domain.StatusActive}, nil
	}
	user, err := svc.VerifyEmail(context.Background(), "user@example.com", "123456")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if user.Status != domain.StatusActive {
		t.Fatalf("expected active status, got %s", user.Status)
	}
}

// AC-4: Resend Verification & Forgot Password
func TestResendVerification_GeneratesOTPForPendingUser(t *testing.T) {
	mailer := &mockMailer{}
	repo := &mockRepository{
		getByEmailFunc: func(ctx context.Context, email string) (*domain.User, error) {
			return &domain.User{ID: "usr-1", Email: email, Status: domain.StatusPendingVerification}, nil
		},
	}
	svc := newTestService(repo, mailer, nil)

	err := svc.ResendVerification(context.Background(), "pending@example.com", "127.0.0.1")
	if err != nil {
		t.Fatalf("ResendVerification failed: %v", err)
	}
	if len(mailer.verificationOTP) != 6 {
		t.Fatalf("expected 6-digit OTP sent to mailer, got %q", mailer.verificationOTP)
	}
}

func TestForgotPassword_GenericResponseForNonExistentUser(t *testing.T) {
	mailer := &mockMailer{}
	repo := &mockRepository{
		getByEmailFunc: func(ctx context.Context, email string) (*domain.User, error) {
			return nil, domain.ErrUserNotFound
		},
	}
	svc := newTestService(repo, mailer, nil)

	// AC-4: Generic success even if user does not exist
	err := svc.ForgotPassword(context.Background(), "nonexistent@example.com", "127.0.0.1")
	if err != nil {
		t.Fatalf("expected nil for non existent user, got %v", err)
	}
	if mailer.resetOTP != "" {
		t.Fatalf("expected no email sent for non existent user, got %q", mailer.resetOTP)
	}
}

// AC-10: Reset Password with 6-digit OTP
func TestResetPassword_ValidatesInputAndUpdatesPassword(t *testing.T) {
	mailer := &mockMailer{}
	var passedNewHash string
	repo := &mockRepository{
		resetPasswordFunc: func(ctx context.Context, email string, otpHash []byte, newHash string, now time.Time) error {
			passedNewHash = newHash
			return nil
		},
	}
	svc := newTestService(repo, mailer, nil)

	// Invalid OTP format
	err := svc.ResetPassword(context.Background(), "user@example.com", "bad-otp", "ValidNewPass1")
	if !errors.Is(err, domain.ErrInvalidOrExpiredToken) {
		t.Fatalf("expected ErrInvalidOrExpiredToken on bad OTP, got %v", err)
	}

	// Invalid short password
	err = svc.ResetPassword(context.Background(), "user@example.com", "123456", "short")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput on short password, got %v", err)
	}

	// Valid reset
	err = svc.ResetPassword(context.Background(), "user@example.com", "123456", "ValidNewPass1")
	if err != nil {
		t.Fatalf("ResetPassword failed: %v", err)
	}
	if !strings.HasPrefix(passedNewHash, "hashed-") {
		t.Fatalf("expected hashed password passed to repo, got %q", passedNewHash)
	}
}

// AC-9: Sign Out
func TestSignOut_RevokesSession(t *testing.T) {
	var revokedSessionID string
	repo := &mockRepository{
		revokeSessionFunc: func(ctx context.Context, userID, sessionID, reason string, now time.Time) error {
			revokedSessionID = sessionID
			return nil
		},
	}
	svc := newTestService(repo, &mockMailer{}, nil)

	err := svc.SignOut(context.Background(), "usr-1", "sess-1")
	if err != nil {
		t.Fatalf("SignOut failed: %v", err)
	}
	if revokedSessionID != "sess-1" {
		t.Fatalf("expected session sess-1 revoked, got %q", revokedSessionID)
	}
}

// AC-11: Change Password
func TestChangePassword_ValidatesCurrentPasswordAndPolicy(t *testing.T) {
	repo := &mockRepository{}
	svc := newTestService(repo, &mockMailer{}, nil)

	// Wrong current password
	err := svc.ChangePassword(context.Background(), "usr-1", "sess-1", "WrongOldPass", "NewValidPass2")
	if !errors.Is(err, domain.ErrInvalidCurrentPassword) {
		t.Fatalf("expected ErrInvalidCurrentPassword, got %v", err)
	}

	// New password same as current
	err = svc.ChangePassword(context.Background(), "usr-1", "sess-1", "OldPass1", "OldPass1")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput when new password equals current, got %v", err)
	}

	// Valid change
	err = svc.ChangePassword(context.Background(), "usr-1", "sess-1", "OldPass1", "NewValidPass2")
	if err != nil {
		t.Fatalf("ChangePassword failed: %v", err)
	}
}
