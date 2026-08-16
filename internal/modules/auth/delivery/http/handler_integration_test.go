package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	authhttp "paysplit-backend/internal/modules/auth/delivery/http"
	authpostgres "paysplit-backend/internal/modules/auth/repository/postgres"
	"paysplit-backend/internal/modules/auth/usecase"
	"paysplit-backend/internal/platform/auth/jwt"
	"paysplit-backend/internal/platform/banks"
	"paysplit-backend/internal/platform/security/password"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type fakeMailer struct{ verification string }

func (m *fakeMailer) SendVerification(_ context.Context, _, _, token string, _ time.Time) error {
	m.verification = token
	return nil
}
func (m *fakeMailer) SendPasswordReset(context.Context, string, string, string, time.Time) error {
	return nil
}

type fakeImages struct{}

func (fakeImages) Convert(_ context.Context, data []byte) ([]byte, error) { return data, nil }
func (fakeImages) IsUnsupported(error) bool                               { return false }

type fakeStorage struct{}

func (fakeStorage) Upload(_ context.Context, _ []byte, key string) (string, error) { return key, nil }
func (fakeStorage) Delete(context.Context, string) error                           { return nil }
func (fakeStorage) URL(key string) string                                          { return "https://images.invalid/" + key }

func TestAuthHTTPJourneyAndRefreshReplay(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const email = "auth.http.test@example.invalid"
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email=$1`, email)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email=$1`, email) })
	repo := authpostgres.New(pool)
	tokenManager, err := jwt.NewAccessTokenManager("integration-secret-longer-than-thirty-two-bytes", "paysplit-test", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := banks.Load()
	if err != nil {
		t.Fatal(err)
	}
	mailer := &fakeMailer{}
	storage := fakeStorage{}
	service := usecase.NewService(repo, password.New(), tokenManager, mailer, directory, fakeImages{}, storage, usecase.Options{VerificationTTL: 10 * time.Minute, ResetTTL: 10 * time.Minute, SessionTTL: 7 * 24 * time.Hour})
	handler := authhttp.NewHandler(service, storage.URL)
	router := chi.NewRouter()
	router.Route("/api/v1", func(api chi.Router) {
		api.Route("/auth", func(r chi.Router) { handler.RegisterAuthRoutes(r, authmw.TokenAuth(tokenManager)) })
		api.Route("/users", func(r chi.Router) { handler.RegisterUserRoutes(r, authmw.Auth(tokenManager, repo)) })
	})
	response := request(t, router, stdhttp.MethodPost, "/api/v1/auth/sign-up", `{"email":"`+email+`","phone_number":"0976543210","display_name":"HTTP Test","password":"StrongPass1"}`, "")
	if response.Code != stdhttp.StatusCreated {
		t.Fatalf("sign up status %d body %s", response.Code, response.Body.String())
	}
	if mailer.verification == "" {
		t.Fatal("verification token was not sent to mailer")
	}
	response = request(t, router, stdhttp.MethodPost, "/api/v1/auth/verify-email", `{"token":"`+mailer.verification+`"}`, "")
	if response.Code != stdhttp.StatusOK {
		t.Fatalf("verify status %d body %s", response.Code, response.Body.String())
	}
	deviceOne := "018f0000-0000-7000-8000-000000000011"
	first := signIn(t, router, email, deviceOne)
	response = request(t, router, stdhttp.MethodGet, "/api/v1/users/me", "", first.AccessToken)
	if response.Code != stdhttp.StatusOK {
		t.Fatalf("profile status %d body %s", response.Code, response.Body.String())
	}
	response = request(t, router, stdhttp.MethodPatch, "/api/v1/users/me", `{"bank_code":"VCB","bank_account_number":"123456789","bank_account_holder":"HTTP TEST"}`, first.AccessToken)
	if response.Code != stdhttp.StatusOK {
		t.Fatalf("profile patch status %d body %s", response.Code, response.Body.String())
	}
	deviceTwo := "018f0000-0000-7000-8000-000000000012"
	second := signIn(t, router, email, deviceTwo)
	response = request(t, router, stdhttp.MethodGet, "/api/v1/users/me", "", first.AccessToken)
	if response.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("old device remains active: %d", response.Code)
	}
	response = request(t, router, stdhttp.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"`+second.RefreshToken+`","device_id":"`+deviceTwo+`"}`, "")
	if response.Code != stdhttp.StatusOK {
		t.Fatalf("refresh status %d body %s", response.Code, response.Body.String())
	}
	var rotated tokenBody
	if err = json.Unmarshal(response.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	response = request(t, router, stdhttp.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"`+second.RefreshToken+`","device_id":"`+deviceTwo+`"}`, "")
	if response.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("refresh replay status %d body %s", response.Code, response.Body.String())
	}
	response = request(t, router, stdhttp.MethodGet, "/api/v1/users/me", "", rotated.AccessToken)
	if response.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("replay did not revoke session: %d", response.Code)
	}
}

type tokenBody struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func signIn(t *testing.T, handler stdhttp.Handler, email, device string) tokenBody {
	t.Helper()
	response := request(t, handler, stdhttp.MethodPost, "/api/v1/auth/sign-in", `{"email":"`+email+`","password":"StrongPass1","device_id":"`+device+`","device_name":"integration"}`, "")
	if response.Code != stdhttp.StatusOK {
		t.Fatalf("sign in status %d body %s", response.Code, response.Body.String())
	}
	var body tokenBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken == "" || body.RefreshToken == "" {
		t.Fatal("missing token pair")
	}
	return body
}
func request(t *testing.T, handler stdhttp.Handler, method, path, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}
