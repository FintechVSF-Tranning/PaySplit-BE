package banks

import (
	"testing"
)

func TestEmbeddedDirectory(t *testing.T) {
	directory, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Test Supported
	if !directory.Supported("VCB") {
		t.Fatal("VCB must be supported")
	}
	if !directory.Supported("vcb") {
		t.Fatal("vcb (lowercase) must be supported")
	}
	if !directory.Supported("  VCB  ") {
		t.Fatal("VCB with whitespace must be supported")
	}
	if directory.Supported("NOT_A_BANK") {
		t.Fatal("unknown bank must not be supported")
	}

	// Test Get
	vcb, ok := directory.Get("VCB")
	if !ok {
		t.Fatal("Get(VCB) must return true")
	}
	if vcb.Code != "VCB" || vcb.BIN != "970436" || vcb.ShortName != "Vietcombank" {
		t.Fatalf("unexpected VCB details: %+v", vcb)
	}
	if !vcb.Supported {
		t.Fatal("VCB must have Supported=true")
	}

	_, ok = directory.Get("UNKNOWN")
	if ok {
		t.Fatal("Get(UNKNOWN) must return false")
	}

	// Test All & List
	all := directory.All()
	if len(all) == 0 {
		t.Fatal("All() must return non-empty slice")
	}

	allViaList := directory.List(nil)
	if len(allViaList) != len(all) {
		t.Fatalf("expected %d banks from List(nil), got %d", len(all), len(allViaList))
	}

	trueVal := true
	supported := directory.List(&trueVal)
	if len(supported) == 0 || len(supported) >= len(all) {
		t.Fatalf("unexpected supported banks count: %d (total %d)", len(supported), len(all))
	}
	for _, b := range supported {
		if !b.Supported {
			t.Fatalf("expected bank %s to be supported", b.Code)
		}
	}

	falseVal := false
	unsupported := directory.List(&falseVal)
	if len(unsupported) == 0 || len(unsupported)+len(supported) != len(all) {
		t.Fatalf("sum of supported (%d) and unsupported (%d) must equal total (%d)", len(supported), len(unsupported), len(all))
	}
	for _, b := range unsupported {
		if b.Supported {
			t.Fatalf("expected bank %s to be unsupported", b.Code)
		}
	}
}

func TestNilDirectory(t *testing.T) {
	var d *Directory
	if d.Supported("VCB") {
		t.Fatal("nil directory Supported must return false")
	}
	if _, ok := d.Get("VCB"); ok {
		t.Fatal("nil directory Get must return false")
	}
	if d.All() != nil {
		t.Fatal("nil directory All must return nil")
	}
	if d.List(nil) != nil {
		t.Fatal("nil directory List must return nil")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name:    "malformed JSON",
			json:    `{invalid`,
			wantErr: "parse embedded banks snapshot",
		},
		{
			name:    "empty data",
			json:    `{"data": []}`,
			wantErr: "embedded banks snapshot is empty",
		},
		{
			name:    "empty bank code",
			json:    `{"data": [{"code": " ", "supported": true}]}`,
			wantErr: "embedded bank code is empty",
		},
		{
			name:    "duplicate bank code",
			json:    `{"data": [{"code": "VCB", "supported": true}, {"code": "VCB", "supported": true}]}`,
			wantErr: "duplicate embedded bank code VCB",
		},
		{
			name:    "no supported banks",
			json:    `{"data": [{"code": "NAB", "supported": false}]}`,
			wantErr: "embedded banks snapshot has no supported banks",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse([]byte(tc.json))
			if err == nil || !containsSubstring(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func containsSubstring(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || stringContains(s, sub)))
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
