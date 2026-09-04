package jobs

// White box test file (package jobs, not jobs_test): ocrInsertOpts is unexported pure logic,
// no River client or DB needed to exercise it directly (Spec 3 AC-3).

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"

	"paysplit-backend/internal/modules/bill/domain"
)

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

// Lỗi provider có thể mang theo nội dung hóa đơn đã bóc tách. Worker chỉ được
// ghi mã lỗi có giới hạn, nếu không log ứng dụng trở thành một bản sao dữ liệu
// nhạy cảm nằm ngoài retention của raw OCR.
func TestOCRErrorCodeNeverEchoesProviderText(t *testing.T) {
	// covers: AC-15
	const sentinel = "SENTINEL-MERCHANT-9f3a total=770000"
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "schema_invalid", err: fmt.Errorf("%w: %s", domain.ErrOcrSchemaInvalid, sentinel), want: ocrErrorSchemaInvalid},
		{name: "unavailable", err: fmt.Errorf("%w: %s", domain.ErrOcrProviderUnavailable, sentinel), want: ocrErrorProviderUnavailable},
		{name: "timeout", err: fmt.Errorf("%w: %s", domain.ErrOcrTimeout, sentinel), want: ocrErrorProviderTimeout},
		{name: "unknown", err: errors.New(sentinel), want: ocrErrorProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := ocrErrorCode(tc.err)
			if code != tc.want {
				t.Fatalf("ocrErrorCode() = %q, want %q", code, tc.want)
			}

			var buf bytes.Buffer
			restore := log.Writer()
			log.SetOutput(&buf)
			log.SetFlags(0)
			log.Printf("event=ocr_provider_failed job_id=%s bill_id=%s attempt=%d err_code=%s", "j", "b", 3, ocrErrorCode(tc.err))
			log.SetOutput(restore)

			if strings.Contains(buf.String(), "SENTINEL-MERCHANT-9f3a") {
				t.Fatalf("provider text reached the log: %q", buf.String())
			}
		})
	}
}
