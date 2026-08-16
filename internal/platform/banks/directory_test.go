package banks

import "testing"

func TestEmbeddedDirectory(t *testing.T) {
	directory, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !directory.Supported("VCB") {
		t.Fatal("VCB must be supported")
	}
	if directory.Supported("NOT_A_BANK") {
		t.Fatal("unknown bank must not be supported")
	}
}
