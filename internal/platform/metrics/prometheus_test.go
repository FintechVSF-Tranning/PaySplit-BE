package metrics_test

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"paysplit-backend/internal/platform/metrics"
)

// histogramSampleCount đọc số lượng observation đã ghi nhận cho một label combination
// của HistogramVec, dùng để xác nhận các hàm Record* thực sự gọi Observe (Spec 3 AC-14).
func histogramSampleCount(t *testing.T, hv *prometheus.HistogramVec, labelValues ...string) uint64 {
	t.Helper()
	m, ok := hv.WithLabelValues(labelValues...).(prometheus.Metric)
	if !ok {
		t.Fatalf("metric for labels %v does not implement prometheus.Metric", labelValues)
	}
	pb := &dto.Metric{}
	if err := m.Write(pb); err != nil {
		t.Fatalf("write histogram metric: %v", err)
	}
	return pb.GetHistogram().GetSampleCount()
}

func TestSetOCRQueueDepth_SetsAbsoluteValue(t *testing.T) {
	// covers: AC-14 (paysplit_ocr_queue_depth is set from a direct DB count by a periodic poller,
	// not incremented/decremented in-process, so it stays correct across restarts and replicas)
	before := testutil.ToFloat64(metrics.OCRQueueDepth)
	t.Cleanup(func() { metrics.SetOCRQueueDepth(before) }) // restore: shared global gauge

	metrics.SetOCRQueueDepth(7)
	if got, want := testutil.ToFloat64(metrics.OCRQueueDepth), 7.0; got != want {
		t.Errorf("SetOCRQueueDepth(7) then ToFloat64() = %v, want %v", got, want)
	}
	metrics.SetOCRQueueDepth(0)
	if got, want := testutil.ToFloat64(metrics.OCRQueueDepth), 0.0; got != want {
		t.Errorf("SetOCRQueueDepth(0) then ToFloat64() = %v, want %v", got, want)
	}
}

func TestRecordOCRJob_IncrementsCounterWithStateAndErrorCodeLabels(t *testing.T) {
	// covers: AC-14 (paysplit_ocr_jobs_total labeled by final application state and cleaned error code)
	before := testutil.ToFloat64(metrics.OCRJobsTotal.WithLabelValues("succeeded_test_case_a", "none"))

	metrics.RecordOCRJob("succeeded_test_case_a", "none")

	got := testutil.ToFloat64(metrics.OCRJobsTotal.WithLabelValues("succeeded_test_case_a", "none"))
	if got != before+1 {
		t.Errorf("RecordOCRJob() counter = %v, want %v", got, before+1)
	}
}

func TestRecordOCRStaleApply_IncrementsCounterWithReasonLabel(t *testing.T) {
	// covers: AC-4, AC-14 (paysplit_ocr_stale_apply_total counts stale candidate apply rejections)
	before := testutil.ToFloat64(metrics.OCRStaleApplyTotal.WithLabelValues("bill_version_changed_test_case_a"))

	metrics.RecordOCRStaleApply("bill_version_changed_test_case_a")
	metrics.RecordOCRStaleApply("bill_version_changed_test_case_a")

	got := testutil.ToFloat64(metrics.OCRStaleApplyTotal.WithLabelValues("bill_version_changed_test_case_a"))
	if got != before+2 {
		t.Errorf("RecordOCRStaleApply() counter = %v, want %v", got, before+2)
	}
}

func TestRecordBillMismatchBlock_IncrementsCounterWithReasonLabel(t *testing.T) {
	// covers: AC-7, AC-9, AC-14 (paysplit_bill_mismatch_block_total counts blocked review/finalize attempts)
	before := testutil.ToFloat64(metrics.BillMismatchBlockTotal.WithLabelValues("totals_mismatch_test_case_a"))

	metrics.RecordBillMismatchBlock("totals_mismatch_test_case_a")

	got := testutil.ToFloat64(metrics.BillMismatchBlockTotal.WithLabelValues("totals_mismatch_test_case_a"))
	if got != before+1 {
		t.Errorf("RecordBillMismatchBlock() counter = %v, want %v", got, before+1)
	}
}

func TestRecordMediaCleanupFailure_IncrementsCounterWithReasonLabel(t *testing.T) {
	// covers: AC-13, AC-14 (paysplit_media_cleanup_failures_total counts media cleanup job failures)
	before := testutil.ToFloat64(metrics.MediaCleanupFailuresTotal.WithLabelValues("delete_failed_test_case_a"))

	metrics.RecordMediaCleanupFailure("delete_failed_test_case_a")

	got := testutil.ToFloat64(metrics.MediaCleanupFailuresTotal.WithLabelValues("delete_failed_test_case_a"))
	if got != before+1 {
		t.Errorf("RecordMediaCleanupFailure() counter = %v, want %v", got, before+1)
	}
}

func TestRecordBillFinalize_ObservesHistogramWithOutcomeLabel(t *testing.T) {
	// covers: AC-9, AC-14 (paysplit_bill_finalize_duration_seconds histogram labeled by outcome)
	beforeSuccess := histogramSampleCount(t, metrics.BillFinalizeDuration, "success")
	beforeError := histogramSampleCount(t, metrics.BillFinalizeDuration, "error")

	metrics.RecordBillFinalize("success", 25*time.Millisecond)
	metrics.RecordBillFinalize("error", 5*time.Millisecond)

	if got, want := histogramSampleCount(t, metrics.BillFinalizeDuration, "success"), beforeSuccess+1; got != want {
		t.Errorf("finalize histogram sample count for outcome=success = %d, want %d", got, want)
	}
	if got, want := histogramSampleCount(t, metrics.BillFinalizeDuration, "error"), beforeError+1; got != want {
		t.Errorf("finalize histogram sample count for outcome=error = %d, want %d", got, want)
	}
}

func TestRecordBillPreview_ObservesHistogramWithOutcomeLabel(t *testing.T) {
	// covers: AC-6, AC-14 (paysplit_bill_preview_duration_seconds histogram labeled by outcome)
	before := histogramSampleCount(t, metrics.BillPreviewDuration, "success")

	metrics.RecordBillPreview("success", 2*time.Millisecond)

	if got, want := histogramSampleCount(t, metrics.BillPreviewDuration, "success"), before+1; got != want {
		t.Errorf("preview histogram sample count for outcome=success = %d, want %d", got, want)
	}
}

func TestRecordOCRProviderDuration_ObservesHistogramWithProviderAndOutcomeLabels(t *testing.T) {
	// covers: AC-3, AC-14 (paysplit_ocr_provider_duration_seconds histogram labeled by provider and outcome)
	before := histogramSampleCount(t, metrics.OCRProviderDuration, "llamaextract_test_case_a", "success")

	metrics.RecordOCRProviderDuration("llamaextract_test_case_a", "success", 3*time.Second)

	if got, want := histogramSampleCount(t, metrics.OCRProviderDuration, "llamaextract_test_case_a", "success"), before+1; got != want {
		t.Errorf("provider duration histogram sample count = %d, want %d", got, want)
	}
}
