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
	"strings"
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
	service := groupusecase.NewService(repo, "https://paysplit.app/join")
	handler := grouphttp.NewHandler(service, func(key string) string { return "https://images.invalid/" + key })

	router := chi.NewRouter()
	liveAuth := authmw.Auth(fakeVerifier{}, fakeSessions{})
	router.Route("/api/v1", func(api chi.Router) {
		api.Route("/groups", func(r chi.Router) {
			handler.RegisterGroupRoutes(r, nil, liveAuth, authmw.RateLimitByAccountAndIP(10000, time.Minute))
		})
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

// decodeJSON decodes the response body and, for a success envelope
// ({"success":true,"data":{...},"message":...}), returns the "data" object
// so existing call sites can keep indexing straight into the payload (e.g.
// body["group"]). Error envelopes ({"success":false,"error":{...}}) have no
// "data" key and are returned as-is so callers can still index body["error"].
func decodeJSON(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode JSON response: %v, body: %s", err, response.Body.String())
	}
	if data, ok := body["data"]; ok {
		if m, ok := data.(map[string]any); ok {
			return m
		}
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
	if got := strings.TrimPrefix(parsedInviteURL.Path, "/join/"); got != code {
		t.Fatalf("invite_url %q does not carry the code as the final path segment (got %q, want %q)", inviteURL, got, code)
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

	// Any active member can share through a truly empty request body and list
	// only available invites. Supplying even a false policy field remains a
	// Captain-only operation (AC-3, AC-10).
	memberReuse := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", "", memberID)
	if memberReuse.Code != stdhttp.StatusOK {
		t.Fatalf("empty member invite request status %d body %s", memberReuse.Code, memberReuse.Body.String())
	}
	if got := decodeJSON(t, memberReuse)["invite"].(map[string]any)["code"]; got != code {
		t.Fatalf("member reused code %v, want %q", got, code)
	}
	memberList := request(t, router, stdhttp.MethodGet, "/api/v1/groups/"+groupID+"/invites", "", memberID)
	if memberList.Code != stdhttp.StatusOK {
		t.Fatalf("member list invites status %d body %s", memberList.Code, memberList.Body.String())
	}
	listed := decodeJSON(t, memberList)["invites"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["code"] != code {
		t.Fatalf("listed invites = %+v, want only current available code %q", listed, code)
	}
	configuredMember := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `{"regenerate":false}`, memberID)
	if configuredMember.Code != stdhttp.StatusForbidden || decodeJSON(t, configuredMember)["error"].(map[string]any)["code"] != "CAPTAIN_REQUIRED" {
		t.Fatalf("configured member response status %d body %s", configuredMember.Code, configuredMember.Body.String())
	}
	malformedMember := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `{"regenerate":"false"}`, memberID)
	if malformedMember.Code != stdhttp.StatusForbidden || decodeJSON(t, malformedMember)["error"].(map[string]any)["code"] != "CAPTAIN_REQUIRED" {
		t.Fatalf("malformed member policy response status %d body %s, want Captain-required 403 before value validation", malformedMember.Code, malformedMember.Body.String())
	}
	malformedCaptain := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `{"regenerate":"false"}`, captainID)
	if malformedCaptain.Code != stdhttp.StatusBadRequest {
		t.Fatalf("malformed Captain policy status %d body %s, want 400 after authorization", malformedCaptain.Code, malformedCaptain.Body.String())
	}
	nullCaptainPolicy := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `{"max_uses":null}`, captainID)
	if nullCaptainPolicy.Code != stdhttp.StatusBadRequest {
		t.Fatalf("null Captain policy status %d body %s, want 400", nullCaptainPolicy.Code, nullCaptainPolicy.Body.String())
	}
	topLevelNull := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `null`, captainID)
	if topLevelNull.Code != stdhttp.StatusBadRequest {
		t.Fatalf("top-level null invite body status %d body %s, want 400", topLevelNull.Code, topLevelNull.Body.String())
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
	fields := errBody["details"].(map[string]any)
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

func TestGroupHTTP_RenameAndDisbandArchiveTheWholeGroup_AC9_AC11(t *testing.T) {
	router, pool := testHandler(t)
	ctx := context.Background()
	captainID := createHTTPTestUser(t, ctx, pool, "Captain")
	memberID := createHTTPTestUser(t, ctx, pool, "Member")

	create := request(t, router, stdhttp.MethodPost, "/api/v1/groups", `{"name":"Old name"}`, captainID)
	if create.Code != stdhttp.StatusCreated {
		t.Fatalf("create group status %d body %s", create.Code, create.Body.String())
	}
	createBody := decodeJSON(t, create)
	groupID := createBody["group"].(map[string]any)["id"].(string)
	captainMembershipID := createBody["membership"].(map[string]any)["id"].(string)
	trackGroupForCleanup(t, pool, groupID)

	invite := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", "", captainID)
	if invite.Code != stdhttp.StatusCreated {
		t.Fatalf("create invite status %d body %s", invite.Code, invite.Body.String())
	}
	inviteBody := decodeJSON(t, invite)["invite"].(map[string]any)
	inviteID := inviteBody["id"].(string)
	code := inviteBody["code"].(string)
	join := request(t, router, stdhttp.MethodPost, "/api/v1/groups/join", `{"code":"`+code+`"}`, memberID)
	if join.Code != stdhttp.StatusOK {
		t.Fatalf("join status %d body %s", join.Code, join.Body.String())
	}
	memberMembershipID := decodeJSON(t, join)["join"].(map[string]any)["membership_id"].(string)

	memberRename := request(t, router, stdhttp.MethodPatch, "/api/v1/groups/"+groupID, `{"name":"No permission"}`, memberID)
	if memberRename.Code != stdhttp.StatusForbidden || decodeJSON(t, memberRename)["error"].(map[string]any)["code"] != "CAPTAIN_REQUIRED" {
		t.Fatalf("member rename status %d body %s", memberRename.Code, memberRename.Body.String())
	}
	rename := request(t, router, stdhttp.MethodPatch, "/api/v1/groups/"+groupID, `{"name":"  Du lịch Đà Lạt  "}`, captainID)
	if rename.Code != stdhttp.StatusOK {
		t.Fatalf("rename status %d body %s", rename.Code, rename.Body.String())
	}
	if got := decodeJSON(t, rename)["group"].(map[string]any)["name"]; got != "Du lịch Đà Lạt" {
		t.Fatalf("renamed group name = %v, want trimmed value", got)
	}
	var renameDescription string
	var renameMetadata []byte
	if err := pool.QueryRow(ctx, `SELECT description,metadata FROM group_activities WHERE group_id=$1 AND action_type='group_renamed'`, groupID).Scan(&renameDescription, &renameMetadata); err != nil {
		t.Fatalf("read rename activity: %v", err)
	}
	if renameDescription != `Captain đã đổi tên nhóm thành "Du lịch Đà Lạt"` {
		t.Fatalf("rename description = %q", renameDescription)
	}
	var renameMeta map[string]any
	if err := json.Unmarshal(renameMetadata, &renameMeta); err != nil {
		t.Fatal(err)
	}
	if renameMeta["old_name"] != "Old name" || renameMeta["new_name"] != "Du lịch Đà Lạt" {
		t.Fatalf("rename metadata = %+v", renameMeta)
	}

	var reviewedBillID, finalizedBillID string
	if err := pool.QueryRow(ctx, `INSERT INTO bills (group_id,creditor_member_id,status,reviewed_at,reviewed_by_member_id) VALUES ($1,$2,'reviewed',now(),$2) RETURNING id`, groupID, captainMembershipID).Scan(&reviewedBillID); err != nil {
		t.Fatalf("insert reviewed bill: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO bills (group_id,creditor_member_id,status,finalized_at) VALUES ($1,$2,'finalized',now()) RETURNING id`, groupID, captainMembershipID).Scan(&finalizedBillID); err != nil {
		t.Fatalf("insert finalized bill: %v", err)
	}
	var debtID string
	if err := pool.QueryRow(ctx, `INSERT INTO debts (group_id,bill_id,debtor_member_id,creditor_member_id,amount,status) VALUES ($1,$2,$3,$4,1000,'awaiting') RETURNING id`, groupID, finalizedBillID, memberMembershipID, captainMembershipID).Scan(&debtID); err != nil {
		t.Fatalf("insert open debt: %v", err)
	}

	blocked := request(t, router, stdhttp.MethodDelete, "/api/v1/groups/"+groupID, "", captainID)
	if blocked.Code != stdhttp.StatusConflict {
		t.Fatalf("blocked disband status %d body %s", blocked.Code, blocked.Body.String())
	}
	blockedError := decodeJSON(t, blocked)["error"].(map[string]any)
	fields := blockedError["details"].(map[string]any)
	if blockedError["code"] != "GROUP_HAS_UNSETTLED_OBLIGATIONS" || fields["draft_or_reviewed_bill_count"] != "1" || fields["open_debt_count"] != "1" {
		t.Fatalf("blocked disband error = %+v", blockedError)
	}

	if _, err := pool.Exec(ctx, `UPDATE bills SET status='voided',finalized_at=now(),voided_at=now() WHERE id=$1`, reviewedBillID); err != nil {
		t.Fatalf("void reviewed bill: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE debts SET status='voided',voided_at=now() WHERE id=$1`, debtID); err != nil {
		t.Fatalf("void debt: %v", err)
	}
	disband := request(t, router, stdhttp.MethodDelete, "/api/v1/groups/"+groupID, "", captainID)
	if disband.Code != stdhttp.StatusNoContent || disband.Body.Len() != 0 {
		t.Fatalf("disband status %d body %q", disband.Code, disband.Body.String())
	}

	var groupStatus string
	var activeMembers, availableInvites, archiveActivities int
	if err := pool.QueryRow(ctx, `SELECT status::text FROM groups WHERE id=$1`, groupID).Scan(&groupStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id=$1 AND status='active'`, groupID).Scan(&activeMembers); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_invites WHERE id=$1 AND revoked_at IS NULL`, inviteID).Scan(&availableInvites); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_activities WHERE group_id=$1 AND action_type='group_archived'`, groupID).Scan(&archiveActivities); err != nil {
		t.Fatal(err)
	}
	if groupStatus != "archived" || activeMembers != 0 || availableInvites != 0 || archiveActivities != 1 {
		t.Fatalf("archive state status=%q active_members=%d available_invites=%d activities=%d", groupStatus, activeMembers, availableInvites, archiveActivities)
	}

	list := request(t, router, stdhttp.MethodGet, "/api/v1/groups", "", captainID)
	if list.Code != stdhttp.StatusOK {
		t.Fatalf("list after archive status %d body %s", list.Code, list.Body.String())
	}
	for _, raw := range decodeJSON(t, list)["groups"].([]any) {
		if raw.(map[string]any)["id"] == groupID {
			t.Fatalf("archived group %s remained in active list", groupID)
		}
	}
	for _, check := range []struct {
		method string
		path   string
		body   string
	}{
		{stdhttp.MethodGet, "/api/v1/groups/" + groupID, ""},
		{stdhttp.MethodPatch, "/api/v1/groups/" + groupID, `{"name":"Again"}`},
		{stdhttp.MethodDelete, "/api/v1/groups/" + groupID, ""},
		{stdhttp.MethodPost, "/api/v1/groups/" + groupID + "/invites", ""},
		{stdhttp.MethodGet, "/api/v1/groups/" + groupID + "/invites", ""},
	} {
		response := request(t, router, check.method, check.path, check.body, captainID)
		if response.Code != stdhttp.StatusNotFound || decodeJSON(t, response)["error"].(map[string]any)["code"] != "GROUP_NOT_FOUND" {
			t.Fatalf("archived %s %s status %d body %s", check.method, check.path, response.Code, response.Body.String())
		}
	}
}

func TestGroupHTTP_InvitePreviewRedactsCodeFromAccessLogPath(t *testing.T) {
	// This exercises the route shape only (the actual log redaction is unit
	// tested directly against internal/transport/http/middleware); confirms
	// the preview route accepts the code as a path segment end to end.
	router, pool := testHandler(t)
	ctx := context.Background()
	captainID := createHTTPTestUser(t, ctx, pool, "Captain")
	outsiderID := createHTTPTestUser(t, ctx, pool, "Outsider")

	create := request(t, router, stdhttp.MethodPost, "/api/v1/groups", `{"name":"Preview Trip"}`, captainID)
	groupID := decodeJSON(t, create)["group"].(map[string]any)["id"].(string)
	trackGroupForCleanup(t, pool, groupID)
	invite := request(t, router, stdhttp.MethodPost, "/api/v1/groups/"+groupID+"/invites", `{}`, captainID)
	code := decodeJSON(t, invite)["invite"].(map[string]any)["code"].(string)

	unknownCode := "ZZZZ9999"
	if code == unknownCode {
		unknownCode = "YYYY8888"
	}
	unknown := request(t, router, stdhttp.MethodGet, "/api/v1/groups/invites/"+unknownCode, "", outsiderID)
	unknownJoin := request(t, router, stdhttp.MethodPost, "/api/v1/groups/join", `{"code":"`+unknownCode+`"}`, outsiderID)
	if unknown.Code != stdhttp.StatusNotFound || unknownJoin.Code != stdhttp.StatusNotFound {
		t.Fatalf("unknown code preview/join statuses %d/%d, want 404/404", unknown.Code, unknownJoin.Code)
	}

	assertUnified := func(label string) {
		t.Helper()
		preview := request(t, router, stdhttp.MethodGet, "/api/v1/groups/invites/"+code, "", outsiderID)
		join := request(t, router, stdhttp.MethodPost, "/api/v1/groups/join", `{"code":"`+code+`"}`, outsiderID)
		if preview.Code != stdhttp.StatusNotFound || preview.Body.String() != unknown.Body.String() {
			t.Fatalf("%s preview status/body = %d/%q, want unknown %d/%q", label, preview.Code, preview.Body.String(), unknown.Code, unknown.Body.String())
		}
		if join.Code != stdhttp.StatusNotFound || join.Body.String() != unknownJoin.Body.String() {
			t.Fatalf("%s join status/body = %d/%q, want unknown %d/%q", label, join.Code, join.Body.String(), unknownJoin.Code, unknownJoin.Body.String())
		}
	}

	inviteID := decodeJSON(t, invite)["invite"].(map[string]any)["id"].(string)
	if _, err := pool.Exec(ctx, `UPDATE group_invites SET expires_at=now()-interval '1 second' WHERE id=$1`, inviteID); err != nil {
		t.Fatal(err)
	}
	assertUnified("expired")
	if _, err := pool.Exec(ctx, `UPDATE group_invites SET expires_at=now()+interval '1 hour',max_uses=1,use_count=1 WHERE id=$1`, inviteID); err != nil {
		t.Fatal(err)
	}
	assertUnified("exhausted")
	if _, err := pool.Exec(ctx, `UPDATE group_invites SET max_uses=NULL,use_count=0 WHERE id=$1`, inviteID); err != nil {
		t.Fatal(err)
	}

	// Revoke then preview: the public result stays byte-for-byte identical to
	// an unknown code so callers cannot distinguish invite state (AC-12).
	revoke := request(t, router, stdhttp.MethodDelete, "/api/v1/groups/"+groupID+"/invites/"+inviteID, "", captainID)
	if revoke.Code != stdhttp.StatusNoContent {
		t.Fatalf("revoke status %d body %s", revoke.Code, revoke.Body.String())
	}
	assertUnified("revoked")
}
