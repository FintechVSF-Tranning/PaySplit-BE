package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	authdomain "paysplit-backend/internal/modules/auth/domain"
	grouphttp "paysplit-backend/internal/modules/group/delivery/http"
	grouppostgres "paysplit-backend/internal/modules/group/repository/postgres"
	groupusecase "paysplit-backend/internal/modules/group/usecase"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

// fakeVerifier and fakeSessions stand in for the real JWT verifier and live
// session store: the bearer token IS the caller's user ID, and every
// session validates. Session/token verification is auth's own concern with
// its own test suite; here it is mocked at that boundary so these tests
// exercise the group HTTP layer against a real Postgres database instead.
type fakeVerifier struct{}

func (fakeVerifier) Verify(token string) (string, string, string, error) {
	if token == "" {
		return "", "", "", fmt.Errorf("empty token")
	}
	return token, "user", "session-" + token, nil
}

type fakeSessions struct{}

func (fakeSessions) ValidateSession(_ context.Context, userID, sessionID string, _ time.Time) (*authdomain.SessionIdentity, error) {
	return &authdomain.SessionIdentity{UserID: userID, Role: "user", SessionID: sessionID}, nil
}

func testHandler(t *testing.T) (stdhttp.Handler, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	repo := grouppostgres.New(pool)
	service := groupusecase.NewService(repo, "paysplit://join")
	handler := grouphttp.NewHandler(service, func(key string) string { return "https://images.invalid/" + key })

	router := chi.NewRouter()
	liveAuth := authmw.Auth(fakeVerifier{}, fakeSessions{})
	router.Route("/api/v1", func(api chi.Router) {
		api.Route("/groups", func(r chi.Router) { handler.RegisterGroupRoutes(r, liveAuth) })
	})
	return router, pool
}

var httpTestUserSeq atomic.Int64

func createHTTPTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, displayName string) string {
	t.Helper()
	seq := httpTestUserSeq.Add(1)
	email := fmt.Sprintf("group.http.test.%d.%d@example.invalid", time.Now().UnixNano(), seq)
	phone := fmt.Sprintf("+848%08d", seq%100000000)
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, display_name, phone_number, role, status, email_verified_at) VALUES ($1,'x',$2,$3,'user','active',now()) RETURNING id`, email, displayName, phone).Scan(&id)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) })
	return id
}

func trackGroupForCleanup(t *testing.T, pool *pgxpool.Pool, groupID string) {
	t.Helper()
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM groups WHERE id=$1`, groupID) })
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

func decodeJSON(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode JSON response: %v, body: %s", err, response.Body.String())
	}
	return body
}

func TestGroupHTTP_FullJourneyCreateInvitePreviewJoinLeaveTransferActivities(t *testing.T) {
	router, pool := testHandler(t)
	ctx := context.Background()
	captainID := createHTTPTestUser(t, ctx, pool, "Captain")
	memberID := createHTTPTestUser(t, ctx, pool, "Member")
	outsiderID := createHTTPTestUser(t, ctx, pool, "Outsider")

	// Create group.
	create := request(t, router, stdhttp.MethodPost, "/api/v1/groups", `{"name":"  HTTP Trip  "}`, captainID)
	if create.Code != stdhttp.StatusCreated {
		t.Fatalf("create group status %d body %s", create.Code, create.Body.String())
	}
	createBody := decodeJSON(t, create)
	group := createBody["group"].(map[string]any)
	membership := createBody["membership"].(map[string]any)
	groupID := group["id"].(string)
	trackGroupForCleanup(t, pool, groupID)
	if group["name"] != "HTTP Trip" {
		t.Fatalf("group.name = %v, want trimmed \"HTTP Trip\"", group["name"])
	}
	if membership["role"] != "captain" || membership["status"] != "active" {
		t.Fatalf("unexpected membership: %+v", membership)
	}

	// List groups.
	list := request(t, router, stdhttp.MethodGet, "/api/v1/groups", "", captainID)
	if list.Code != stdhttp.StatusOK {
		t.Fatalf("list groups status %d body %s", list.Code, list.Body.String())
	}
	listBody := decodeJSON(t, list)
	groups := listBody["groups"].([]any)
	if len(groups) < 1 {
		t.Fatalf("list groups returned no groups: %s", list.Body.String())
	}

	// Detail as captain.
	detail := request(t, router, stdhttp.MethodGet, "/api/v1/groups/"+groupID, "", captainID)
	if detail.Code != stdhttp.StatusOK {
		t.Fatalf("detail status %d body %s", detail.Code, detail.Body.String())
	}
	// Detail as nonmember and as nonexistent group both 404, identical body.
	notFoundNonMember := request(t, router, stdhttp.MethodGet, "/api/v1/groups/"+groupID, "", outsiderID)
	notFoundMissing := request(t, router, stdhttp.MethodGet, "/api/v1/groups/018f0000-0000-7000-8000-000000000000", "", outsiderID)
	if notFoundNonMember.Code != stdhttp.StatusNotFound || notFoundMissing.Code != stdhttp.StatusNotFound {
		t.Fatalf("expected 404 for both nonmember (%d) and missing group (%d)", notFoundNonMember.Code, notFoundMissing.Code)
	}
	if notFoundNonMember.Body.String() != notFoundMissing.Body.String() {
		t.Fatalf("nonmember body %q != missing group body %q, group existence would be leaked", notFoundNonMember.Body.String(), notFoundMissing.Body.String())
	}

	// Create invite.
	invite := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `{}`, captainID)
	if invite.Code != stdhttp.StatusCreated {
		t.Fatalf("create invite status %d body %s", invite.Code, invite.Body.String())
	}
	inviteBody := decodeJSON(t, invite)["invite"].(map[string]any)
	code := inviteBody["code"].(string)
	if code == "" {
		t.Fatal("invite.code is empty")
	}
	inviteURL := inviteBody["invite_url"].(string)
	parsedInviteURL, err := url.Parse(inviteURL)
	if err != nil {
		t.Fatalf("invite_url %q is not a parseable URL: %v", inviteURL, err)
	}
	if got := parsedInviteURL.Query().Get("code"); got != code {
		t.Fatalf("invite_url %q does not carry the code back out as a query parameter (got %q, want %q)", inviteURL, got, code)
	}

	// Reuse invite: 200, same code.
	reuse := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `{}`, captainID)
	if reuse.Code != stdhttp.StatusOK {
		t.Fatalf("reuse invite status %d body %s", reuse.Code, reuse.Body.String())
	}
	reuseBody := decodeJSON(t, reuse)["invite"].(map[string]any)
	if reuseBody["code"] != code {
		t.Fatalf("reused invite code %v != original %v", reuseBody["code"], code)
	}

	// Preview as nonmember.
	preview := request(t, router, stdhttp.MethodGet, "/api/v1/groups/invites/"+code, "", memberID)
	if preview.Code != stdhttp.StatusOK {
		t.Fatalf("preview status %d body %s", preview.Code, preview.Body.String())
	}
	previewBody := decodeJSON(t, preview)["preview"].(map[string]any)
	if previewBody["group_name"] != "HTTP Trip" || previewBody["captain_display_name"] != "Captain" {
		t.Fatalf("unexpected preview: %+v", previewBody)
	}

	// Join.
	join := request(t, router, stdhttp.MethodPost, "/api/v1/groups/join", `{"code":"`+code+`"}`, memberID)
	if join.Code != stdhttp.StatusOK {
		t.Fatalf("join status %d body %s", join.Code, join.Body.String())
	}
	joinBody := decodeJSON(t, join)["join"].(map[string]any)
	if joinBody["result"] != "joined" {
		t.Fatalf("join.result = %v, want joined", joinBody["result"])
	}
	memberMembershipID := joinBody["membership_id"].(string)

	// Idempotent repeat join.
	joinAgain := request(t, router, stdhttp.MethodPost, "/api/v1/groups/join", `{"code":"`+code+`"}`, memberID)
	if joinAgain.Code != stdhttp.StatusOK {
		t.Fatalf("repeat join status %d body %s", joinAgain.Code, joinAgain.Body.String())
	}
	if decodeJSON(t, joinAgain)["join"].(map[string]any)["result"] != "already_active" {
		t.Fatalf("repeat join result = %v, want already_active", decodeJSON(t, joinAgain)["join"])
	}

	// Activities timeline: at least group_created, invite_created x2, member_joined.
	activities := request(t, router, stdhttp.MethodGet, "/api/v1/groups/"+groupID+"/activities", "", captainID)
	if activities.Code != stdhttp.StatusOK {
		t.Fatalf("activities status %d body %s", activities.Code, activities.Body.String())
	}
	activityList := decodeJSON(t, activities)["activities"].([]any)
	if len(activityList) < 3 {
		t.Fatalf("activities = %d, want at least 3", len(activityList))
	}
	first := activityList[0].(map[string]any)
	if _, ok := first["actor"].(map[string]any)["display_name"]; !ok {
		t.Fatalf("activity actor missing display_name: %+v", first)
	}

	// Nonmember forbidden from activities.
	forbiddenActivities := request(t, router, stdhttp.MethodGet, "/api/v1/groups/"+groupID+"/activities", "", outsiderID)
	if forbiddenActivities.Code != stdhttp.StatusForbidden {
		t.Fatalf("nonmember activities status %d, want 403", forbiddenActivities.Code)
	}

	// Transfer Captain to the member.
	transfer := request(t, router, stdhttp.MethodPut, "/api/v1/groups/"+groupID+"/members/"+memberMembershipID+"/role", `{"role":"captain"}`, captainID)
	if transfer.Code != stdhttp.StatusOK {
		t.Fatalf("transfer status %d body %s", transfer.Code, transfer.Body.String())
	}
	transferBody := decodeJSON(t, transfer)["transfer"].(map[string]any)
	if transferBody["current_captain_member_id"] != memberMembershipID {
		t.Fatalf("current_captain_member_id = %v, want %q", transferBody["current_captain_member_id"], memberMembershipID)
	}

	// Old Captain (now a standard member) can leave.
	oldCaptainMembershipID := membership["id"].(string)
	leave := request(t, router, stdhttp.MethodDelete, "/api/v1/groups/"+groupID+"/members/"+oldCaptainMembershipID, "", captainID)
	if leave.Code != stdhttp.StatusNoContent {
		t.Fatalf("leave status %d body %s", leave.Code, leave.Body.String())
	}
	// Idempotent retry.
	leaveAgain := request(t, router, stdhttp.MethodDelete, "/api/v1/groups/"+groupID+"/members/"+oldCaptainMembershipID, "", captainID)
	if leaveAgain.Code != stdhttp.StatusNoContent {
		t.Fatalf("idempotent leave retry status %d body %s", leaveAgain.Code, leaveAgain.Body.String())
	}
}

func TestGroupHTTP_RequiresAuthentication(t *testing.T) {
	router, _ := testHandler(t)
	response := request(t, router, stdhttp.MethodGet, "/api/v1/groups", "", "")
	if response.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 with no bearer token", response.Code)
	}
}

func TestGroupHTTP_CreateGroupRejectsInvalidBody(t *testing.T) {
	router, pool := testHandler(t)
	ctx := context.Background()
	userID := createHTTPTestUser(t, ctx, pool, "Validator")

	blank := request(t, router, stdhttp.MethodPost, "/api/v1/groups", `{"name":"   "}`, userID)
	if blank.Code != stdhttp.StatusBadRequest {
		t.Fatalf("blank name status %d, want 400", blank.Code)
	}
	body := decodeJSON(t, blank)
	errBody := body["error"].(map[string]any)
	if errBody["code"] != "VALIDATION_FAILED" {
		t.Fatalf("error.code = %v, want VALIDATION_FAILED", errBody["code"])
	}

	nonVND := request(t, router, stdhttp.MethodPost, "/api/v1/groups", `{"name":"Trip","currency":"USD"}`, userID)
	if nonVND.Code != stdhttp.StatusBadRequest {
		t.Fatalf("non-VND currency status %d, want 400", nonVND.Code)
	}
}

func TestGroupHTTP_LeaveOnOpenDebtsReturnsAmountsInErrorFields(t *testing.T) {
	router, pool := testHandler(t)
	ctx := context.Background()
	captainID := createHTTPTestUser(t, ctx, pool, "Captain")
	memberID := createHTTPTestUser(t, ctx, pool, "Debtor")

	create := request(t, router, stdhttp.MethodPost, "/api/v1/groups", `{"name":"Debt Trip"}`, captainID)
	if create.Code != stdhttp.StatusCreated {
		t.Fatalf("create group status %d body %s", create.Code, create.Body.String())
	}
	createBody := decodeJSON(t, create)
	group := createBody["group"].(map[string]any)
	groupID := group["id"].(string)
	trackGroupForCleanup(t, pool, groupID)
	captainMembershipID := createBody["membership"].(map[string]any)["id"].(string)

	invite := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `{}`, captainID)
	code := decodeJSON(t, invite)["invite"].(map[string]any)["code"].(string)
	join := request(t, router, stdhttp.MethodPost, "/api/v1/groups/join", `{"code":"`+code+`"}`, memberID)
	memberMembershipID := decodeJSON(t, join)["join"].(map[string]any)["membership_id"].(string)

	var billID string
	if err := pool.QueryRow(ctx, `INSERT INTO bills (group_id, creditor_member_id, status, finalized_at) VALUES ($1,$2,'finalized',now()) RETURNING id`, groupID, captainMembershipID).Scan(&billID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO debts (group_id, bill_id, debtor_member_id, creditor_member_id, amount, status) VALUES ($1,$2,$3,$4,42000,'awaiting')`, groupID, billID, memberMembershipID, captainMembershipID); err != nil {
		t.Fatal(err)
	}

	leave := request(t, router, stdhttp.MethodDelete, "/api/v1/groups/"+groupID+"/members/"+memberMembershipID, "", memberID)
	if leave.Code != stdhttp.StatusConflict {
		t.Fatalf("leave with open debt status %d body %s", leave.Code, leave.Body.String())
	}
	body := decodeJSON(t, leave)
	errBody := body["error"].(map[string]any)
	if errBody["code"] != "GROUP_MEMBER_HAS_OPEN_DEBTS" {
		t.Fatalf("error.code = %v, want GROUP_MEMBER_HAS_OPEN_DEBTS", errBody["code"])
	}
	fields := errBody["fields"].(map[string]any)
	if fields["payable_amount"] != "42000" || fields["receivable_amount"] != "0" {
		t.Fatalf("error.fields = %+v, want payable_amount 42000 and receivable_amount 0", fields)
	}
}

func TestGroupHTTP_CaptainSelfLeaveRequiresTransferFirst(t *testing.T) {
	router, pool := testHandler(t)
	ctx := context.Background()
	captainID := createHTTPTestUser(t, ctx, pool, "Captain")

	create := request(t, router, stdhttp.MethodPost, "/api/v1/groups", `{"name":"Solo Trip"}`, captainID)
	createBody := decodeJSON(t, create)
	groupID := createBody["group"].(map[string]any)["id"].(string)
	trackGroupForCleanup(t, pool, groupID)
	captainMembershipID := createBody["membership"].(map[string]any)["id"].(string)

	leave := request(t, router, stdhttp.MethodDelete, "/api/v1/groups/"+groupID+"/members/"+captainMembershipID, "", captainID)
	if leave.Code != stdhttp.StatusConflict {
		t.Fatalf("captain self-leave status %d body %s, want 409", leave.Code, leave.Body.String())
	}
	if decodeJSON(t, leave)["error"].(map[string]any)["code"] != "CAPTAIN_TRANSFER_REQUIRED" {
		t.Fatalf("error.code = %v, want CAPTAIN_TRANSFER_REQUIRED", decodeJSON(t, leave)["error"])
	}
}

func TestGroupHTTP_InvitePreviewRedactsCodeFromAccessLogPath(t *testing.T) {
	// This exercises the route shape only (the actual log redaction is unit
	// tested directly against internal/transport/http/middleware); confirms
	// the preview route accepts the code as a path segment end to end.
	router, pool := testHandler(t)
	ctx := context.Background()
	captainID := createHTTPTestUser(t, ctx, pool, "Captain")

	create := request(t, router, stdhttp.MethodPost, "/api/v1/groups", `{"name":"Preview Trip"}`, captainID)
	groupID := decodeJSON(t, create)["group"].(map[string]any)["id"].(string)
	trackGroupForCleanup(t, pool, groupID)
	invite := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `{}`, captainID)
	code := decodeJSON(t, invite)["invite"].(map[string]any)["code"].(string)

	unknown := request(t, router, stdhttp.MethodGet, "/api/v1/groups/invites/does-not-exist", "", captainID)
	if unknown.Code != stdhttp.StatusNotFound {
		t.Fatalf("unknown code status %d, want 404", unknown.Code)
	}

	// Revoke then preview: 410.
	inviteID := decodeJSON(t, invite)["invite"].(map[string]any)["id"].(string)
	revoke := request(t, router, stdhttp.MethodDelete, "/api/v1/groups/"+groupID+"/invites/"+inviteID, "", captainID)
	if revoke.Code != stdhttp.StatusNoContent {
		t.Fatalf("revoke status %d body %s", revoke.Code, revoke.Body.String())
	}
	unavailable := request(t, router, stdhttp.MethodGet, "/api/v1/groups/invites/"+code, "", captainID)
	if unavailable.Code != stdhttp.StatusGone {
		t.Fatalf("revoked code preview status %d, want 410", unavailable.Code)
	}
}
