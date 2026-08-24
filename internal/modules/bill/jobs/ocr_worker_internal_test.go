package jobs

// White box test file (package jobs, not jobs_test): ocrInsertOpts is unexported pure logic,
// no River client or DB needed to exercise it directly (Spec 3 AC-3).

import "testing"

func TestEnqueuer_OcrInsertOpts_UsesConfiguredMaxAttempts(t *testing.T) {
	e := &Enqueuer{ocrMaxAttempts: 3}

	opts := e.ocrInsertOpts()

	if opts == nil {
		t.Fatal("expected non-nil InsertOpts when ocrMaxAttempts is configured")
	}
	if opts.MaxAttempts != 3 {
		t.Errorf("InsertOpts.MaxAttempts = %d, want 3", opts.MaxAttempts)
	}
}

func TestEnqueuer_OcrInsertOpts_UnconfiguredMaxAttempts_UsesRiverDefault(t *testing.T) {
	e := &Enqueuer{ocrMaxAttempts: 0}

	if opts := e.ocrInsertOpts(); opts != nil {
		t.Errorf("expected nil InsertOpts (River default) when ocrMaxAttempts is unset, got %+v", opts)
	}
}
