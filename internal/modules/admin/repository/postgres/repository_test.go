package postgres

import "testing"

// TestAuditActionForStatus covers AC-3/AC-4: admin_audit_logs.action uses the enum
// ('suspend','lock','reactivate'), distinct from users.status ('suspended','locked','active').
// A prior bug passed users.status straight through, which violated the enum and rolled back
// every status change transaction. This is a plain unit test (no TEST_DATABASE_URL needed) so
// the mapping stays covered in a default `go test ./...` run, not only via the DB-gated
// integration test.
func TestAuditActionForStatus(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"suspended", "suspend"},
		{"locked", "lock"},
		{"active", "reactivate"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			if got := auditActionForStatus(tc.status); got != tc.want {
				t.Errorf("auditActionForStatus(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

func TestMaskBankAccount(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"typical account number", "9876543211234", "******1234"},
		{"exactly four digits", "1234", "******1234"},
		{"fewer than four digits", "12", "******12"},
		{"trims surrounding whitespace", " 9876543211234 ", "******1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskBankAccount(tc.in); got != tc.want {
				t.Errorf("MaskBankAccount(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
