package jwt

import (
	"testing"
	"time"
)

func TestAccessTokenCarriesSessionID(t *testing.T) {
	manager, err := NewAccessTokenManager("a-development-secret-longer-than-32-bytes", "paysplit-test", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, expires, err := manager.Issue("018f0000-0000-7000-8000-000000000001", RoleUser, "018f0000-0000-7000-8000-000000000002")
	if err != nil {
		t.Fatal(err)
	}
	user, role, session, err := manager.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if user != "018f0000-0000-7000-8000-000000000001" || role != RoleUser || session != "018f0000-0000-7000-8000-000000000002" {
		t.Fatalf("unexpected claims %s %s %s", user, role, session)
	}
	remaining := time.Until(expires)
	if remaining < 14*time.Minute || remaining > 15*time.Minute {
		t.Fatalf("unexpected TTL %s", remaining)
	}
}
