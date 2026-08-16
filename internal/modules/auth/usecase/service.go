package usecase

import (
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"net"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/nyaruka/phonenumbers"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"
)

type PasswordManager interface {
	Validate(string) error
	Hash(string) (string, error)
	Compare(string, string) error
}

type TokenIssuer interface {
	Issue(userID, role, sessionID string) (string, time.Time, error)
}

type Mailer interface {
	SendVerification(context.Context, string, string, string, time.Time) error
	SendPasswordReset(context.Context, string, string, string, time.Time) error
}

type BankDirectory interface{ Supported(string) bool }

type ImageProcessor interface {
	Convert(context.Context, []byte) ([]byte, error)
	IsUnsupported(error) bool
}

type AvatarStorage interface {
	Upload(context.Context, []byte, string) (string, error)
	Delete(context.Context, string) error
	URL(string) string
}

type Service struct {
	repo            repository.Repository
	passwords       PasswordManager
	tokens          TokenIssuer
	mailer          Mailer
	banks           BankDirectory
	images          ImageProcessor
	avatars         AvatarStorage
	verificationTTL time.Duration
	resetTTL        time.Duration
	sessionTTL      time.Duration
}

type Options struct{ VerificationTTL, ResetTTL, SessionTTL time.Duration }

func NewService(repo repository.Repository, passwords PasswordManager, tokens TokenIssuer, mailer Mailer, banks BankDirectory, images ImageProcessor, avatars AvatarStorage, opts Options) *Service {
	if repo == nil || passwords == nil || tokens == nil || mailer == nil || banks == nil || images == nil || avatars == nil {
		panic("auth service dependencies must not be nil")
	}
	if opts.VerificationTTL <= 0 || opts.ResetTTL <= 0 || opts.SessionTTL <= 0 {
		panic("auth service TTL values must be positive")
	}
	return &Service{repo: repo, passwords: passwords, tokens: tokens, mailer: mailer, banks: banks, images: images, avatars: avatars, verificationTTL: opts.VerificationTTL, resetTTL: opts.ResetTTL, sessionTTL: opts.SessionTTL}
}

type SignUpInput struct{ Email, PhoneNumber, DisplayName, Password, ClientIP string }
type SignUpOutput struct {
	User                  *domain.User
	VerificationEmailSent bool
	VerificationExpiresAt time.Time
}

func (s *Service) SignUp(ctx context.Context, in SignUpInput) (*SignUpOutput, error) {
	now := time.Now()
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return nil, domain.ErrInvalidInput
	}
	phone, err := normalizePhone(in.PhoneNumber)
	if err != nil {
		return nil, domain.ErrInvalidInput
	}
	name, err := normalizeName(in.DisplayName, 100)
	if err != nil {
		return nil, domain.ErrInvalidInput
	}
	if err = s.passwords.Validate(in.Password); err != nil {
		return nil, domain.ErrInvalidInput
	}
	if _, err = s.repo.CheckAndRecordRateLimit(ctx, "sign_up", map[string][]byte{"ip": hashKey(canonicalIP(in.ClientIP))}, now); err != nil {
		return nil, err
	}
	hash, err := s.passwords.Hash(in.Password)
	if err != nil {
		return nil, err
	}
	raw, tokenHash, err := domain.NewOpaqueToken()
	if err != nil {
		return nil, err
	}
	expires := now.Add(s.verificationTTL)
	user, err := s.repo.CreateUser(ctx, repository.CreateUserParams{Email: email, PhoneNumber: phone, DisplayName: name, PasswordHash: hash, VerificationTokenHash: tokenHash, VerificationExpiresAt: expires})
	if err != nil {
		return nil, err
	}
	mailErr := s.mailer.SendVerification(ctx, user.Email, user.DisplayName, raw, expires)
	sent := mailErr == nil
	if mailErr != nil {
		log.Printf("event=verification_email_send_failed user_id=%s", user.ID)
	}
	return &SignUpOutput{User: user, VerificationEmailSent: sent, VerificationExpiresAt: expires}, nil
}

func (s *Service) VerifyEmail(ctx context.Context, raw string) (*domain.User, error) {
	if !validRawToken(raw) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	return s.repo.VerifyEmail(ctx, domain.HashToken(raw), time.Now())
}

func (s *Service) ResendVerification(ctx context.Context, email, clientIP string) error {
	return s.sendUserToken(ctx, email, clientIP, domain.TokenEmailVerification)
}
func (s *Service) ForgotPassword(ctx context.Context, email, clientIP string) error {
	return s.sendUserToken(ctx, email, clientIP, domain.TokenPasswordReset)
}

func (s *Service) sendUserToken(ctx context.Context, inputEmail, clientIP, kind string) error {
	email, err := normalizeEmail(inputEmail)
	if err != nil {
		return domain.ErrInvalidInput
	}
	now := time.Now()
	action := "resend_verification"
	ttl := s.verificationTTL
	if kind == domain.TokenPasswordReset {
		action = "forgot_password"
		ttl = s.resetTTL
	}
	keys := map[string][]byte{"email": hashKey(email), "ip": hashKey(canonicalIP(clientIP))}
	if _, err = s.repo.CheckAndRecordRateLimit(ctx, action, keys, now); err != nil {
		return err
	}
	user, err := s.repo.GetByEmail(ctx, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if kind == domain.TokenEmailVerification && user.Status != domain.StatusPendingVerification {
		return nil
	}
	raw, hash, err := domain.NewOpaqueToken()
	if err != nil {
		return err
	}
	expires := now.Add(ttl)
	if err = s.repo.CreateUserToken(ctx, user.ID, kind, hash, expires); err != nil {
		return err
	}
	if kind == domain.TokenEmailVerification {
		if err = s.mailer.SendVerification(ctx, user.Email, user.DisplayName, raw, expires); err != nil {
			log.Printf("event=verification_email_send_failed user_id=%s", user.ID)
		}
	} else {
		if err = s.mailer.SendPasswordReset(ctx, user.Email, user.DisplayName, raw, expires); err != nil {
			log.Printf("event=password_reset_email_send_failed user_id=%s", user.ID)
		}
	}
	return nil
}

type SignInInput struct{ Email, Password, DeviceID, DeviceName string }
type TokenOutput struct {
	User                              *domain.User
	AccessToken, RefreshToken         string
	AccessExpiresAt, RefreshExpiresAt time.Time
}

func (s *Service) SignIn(ctx context.Context, in SignInInput) (*TokenOutput, error) {
	email, err := normalizeEmail(in.Email)
	if err != nil || in.Password == "" {
		return nil, domain.ErrInvalidCredentials
	}
	deviceID, err := canonicalUUID(in.DeviceID)
	if err != nil {
		return nil, domain.ErrInvalidInput
	}
	deviceName := strings.TrimSpace(in.DeviceName)
	if utf8.RuneCountInString(deviceName) > 120 {
		return nil, domain.ErrInvalidInput
	}
	now := time.Now()
	user, err := s.repo.GetByEmail(ctx, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		_, _ = s.repo.RecordLoginFailure(ctx, email, now)
		return nil, domain.ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if user.LoginBlockedUntil != nil && now.Before(*user.LoginBlockedUntil) {
		return nil, &domain.RateLimitError{RetryAfter: user.LoginBlockedUntil.Sub(now)}
	}
	if err = s.passwords.Compare(user.PasswordHash, in.Password); err != nil {
		retry, recordErr := s.repo.RecordLoginFailure(ctx, email, now)
		if recordErr != nil {
			return nil, recordErr
		}
		if retry > 0 {
			return nil, &domain.RateLimitError{RetryAfter: retry}
		}
		return nil, domain.ErrInvalidCredentials
	}
	refresh, refreshHash, err := domain.NewOpaqueToken()
	if err != nil {
		return nil, err
	}
	sessionExpires := now.Add(s.sessionTTL)
	user, session, err := s.repo.CreateSession(ctx, repository.CreateSessionParams{UserID: user.ID, ExpectedPasswordHash: user.PasswordHash, DeviceID: deviceID, DeviceName: deviceName, RefreshTokenHash: refreshHash, Now: now, ExpiresAt: sessionExpires})
	if err != nil {
		return nil, err
	}
	access, accessExpiry, err := s.tokens.Issue(user.ID, user.Role, session.ID)
	if err != nil {
		_ = s.repo.RevokeSession(ctx, user.ID, session.ID, "access_issue_failed", time.Now())
		return nil, err
	}
	return &TokenOutput{User: user, AccessToken: access, RefreshToken: refresh, AccessExpiresAt: accessExpiry, RefreshExpiresAt: session.ExpiresAt}, nil
}

func (s *Service) Refresh(ctx context.Context, raw, device string) (*TokenOutput, error) {
	if !validRawToken(raw) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	deviceID, err := canonicalUUID(device)
	if err != nil {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	next, nextHash, err := domain.NewOpaqueToken()
	if err != nil {
		return nil, err
	}
	rotated, err := s.repo.RotateRefresh(ctx, domain.HashToken(raw), nextHash, deviceID, time.Now())
	if err != nil {
		return nil, err
	}
	access, accessExpiry, err := s.tokens.Issue(rotated.User.ID, rotated.User.Role, rotated.SessionID)
	if err != nil {
		return nil, err
	}
	return &TokenOutput{AccessToken: access, RefreshToken: next, AccessExpiresAt: accessExpiry, RefreshExpiresAt: rotated.ExpiresAt}, nil
}

func (s *Service) SignOut(ctx context.Context, userID, sessionID string) error {
	return s.repo.RevokeSession(ctx, userID, sessionID, "sign_out", time.Now())
}

func (s *Service) ResetPassword(ctx context.Context, raw, newPassword string) error {
	if !validRawToken(raw) {
		return domain.ErrInvalidOrExpiredToken
	}
	if err := s.passwords.Validate(newPassword); err != nil {
		return domain.ErrInvalidInput
	}
	hash, err := s.passwords.Hash(newPassword)
	if err != nil {
		return err
	}
	return s.repo.ResetPassword(ctx, domain.HashToken(raw), hash, time.Now())
}

func (s *Service) ChangePassword(ctx context.Context, userID, sessionID, current, next string) error {
	if err := s.passwords.Validate(next); err != nil {
		return domain.ErrInvalidInput
	}
	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if s.passwords.Compare(user.PasswordHash, current) != nil {
		return domain.ErrInvalidCurrentPassword
	}
	if s.passwords.Compare(user.PasswordHash, next) == nil {
		return domain.ErrInvalidInput
	}
	hash, err := s.passwords.Hash(next)
	if err != nil {
		return err
	}
	return s.repo.ChangePassword(ctx, userID, sessionID, hash, time.Now())
}

func (s *Service) GetProfile(ctx context.Context, userID string) (*domain.User, error) {
	return s.repo.GetByID(ctx, userID)
}

func (s *Service) UploadAvatar(ctx context.Context, userID string, input []byte) (*domain.User, error) {
	if len(input) == 0 {
		return nil, domain.ErrInvalidImage
	}
	if len(input) > 10<<20 {
		return nil, domain.ErrPayloadTooLarge
	}
	payload, err := s.images.Convert(ctx, input)
	if err != nil {
		if s.images.IsUnsupported(err) {
			payload = input
		} else {
			return nil, domain.ErrInvalidImage
		}
	}
	suffix, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	publicID := "paysplit/avatars/" + userID + "/" + suffix.String()
	stored, err := s.avatars.Upload(ctx, payload, publicID)
	if err != nil {
		return nil, domain.ErrImageStorage
	}
	user, old, err := s.repo.SetAvatar(ctx, userID, stored)
	if err != nil {
		_ = s.avatars.Delete(context.Background(), stored)
		return nil, err
	}
	if old != nil && *old != "" {
		if err = s.avatars.Delete(context.Background(), *old); err != nil {
			_ = s.repo.EnqueueMediaCleanup(context.Background(), "cloudinary", *old)
			log.Printf("event=avatar_cleanup_queued user_id=%s", userID)
		}
	}
	return user, nil
}

func (s *Service) DeleteAvatar(ctx context.Context, userID string) error {
	old, err := s.repo.ClearAvatar(ctx, userID)
	if err != nil {
		return err
	}
	if old != nil && *old != "" {
		if err = s.avatars.Delete(context.Background(), *old); err != nil {
			_ = s.repo.EnqueueMediaCleanup(context.Background(), "cloudinary", *old)
			log.Printf("event=avatar_cleanup_queued user_id=%s", userID)
		}
	}
	return nil
}

type PatchProfileInput struct {
	DisplayName, PhoneNumber *string
	Bank                     *domain.BankProfile
}

func (s *Service) PatchProfile(ctx context.Context, userID string, in PatchProfileInput) (*domain.User, error) {
	p := repository.ProfilePatch{Bank: in.Bank}
	if in.DisplayName != nil {
		name, err := normalizeName(*in.DisplayName, 100)
		if err != nil {
			return nil, domain.ErrInvalidInput
		}
		p.DisplayName = &name
	}
	if in.PhoneNumber != nil {
		phone, err := normalizePhone(*in.PhoneNumber)
		if err != nil {
			return nil, domain.ErrInvalidInput
		}
		p.PhoneNumber = &phone
	}
	if p.Bank != nil {
		if !validBankGroup(p.Bank) {
			return nil, domain.ErrInvalidInput
		}
		if p.Bank.Code != nil && !s.banks.Supported(*p.Bank.Code) {
			return nil, domain.ErrUnsupportedBank
		}
	}
	return s.repo.UpdateProfile(ctx, userID, p)
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 254 {
		return "", domain.ErrInvalidInput
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || strings.ToLower(parsed.Address) != value {
		return "", domain.ErrInvalidInput
	}
	return value, nil
}
func normalizePhone(value string) (string, error) {
	parsed, err := phonenumbers.Parse(strings.TrimSpace(value), "VN")
	if err != nil || !phonenumbers.IsValidNumber(parsed) {
		return "", domain.ErrInvalidInput
	}
	out := phonenumbers.Format(parsed, phonenumbers.E164)
	if len(out) > 16 {
		return "", domain.ErrInvalidInput
	}
	return out, nil
}
func normalizeName(value string, max int) (string, error) {
	value = strings.TrimSpace(value)
	n := utf8.RuneCountInString(value)
	if n < 1 || n > max {
		return "", domain.ErrInvalidInput
	}
	return value, nil
}
func canonicalUUID(value string) (string, error) {
	v, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return v.String(), nil
}
func canonicalIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if parsed := net.ParseIP(value); parsed != nil {
		return parsed.String()
	}
	return strings.ToLower(value)
}
func hashKey(value string) []byte     { sum := sha256.Sum256([]byte(value)); return sum[:] }
func validRawToken(value string) bool { return len(value) > 0 && len(value) <= 128 }
func validBankGroup(b *domain.BankProfile) bool {
	if b == nil {
		return true
	}
	allNil := b.Code == nil && b.AccountNumber == nil && b.AccountHolder == nil
	if allNil {
		return true
	}
	if b.Code == nil || b.AccountNumber == nil || b.AccountHolder == nil {
		return false
	}
	code := strings.TrimSpace(*b.Code)
	number := strings.TrimSpace(*b.AccountNumber)
	holder := strings.TrimSpace(*b.AccountHolder)
	if code == "" || len(number) < 6 || len(number) > 19 {
		return false
	}
	for _, r := range number {
		if r < '0' || r > '9' {
			return false
		}
	}
	if utf8.RuneCountInString(holder) < 1 || utf8.RuneCountInString(holder) > 100 {
		return false
	}
	code = strings.ToUpper(code)
	b.Code = &code
	b.AccountNumber = &number
	b.AccountHolder = &holder
	return true
}
