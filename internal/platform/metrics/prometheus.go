package metrics

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"paysplit-backend/internal/transport/http/helpers"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total number of HTTP requests processed, labeled by method, route, and status code.",
		},
		[]string{"method", "route", "status_code"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "paysplit",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "Histogram of HTTP request latencies in seconds.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method", "route"},
	)

	dbPoolAcquiredConns = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "paysplit",
			Subsystem: "db",
			Name:      "pool_acquired_conns",
			Help:      "Number of currently acquired connections from the PostgreSQL pool.",
		},
		func() float64 {
			if activePool != nil {
				return float64(activePool.Stat().AcquiredConns())
			}
			return 0
		},
	)

	dbPoolIdleConns = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "paysplit",
			Subsystem: "db",
			Name:      "pool_idle_conns",
			Help:      "Number of currently idle connections in the PostgreSQL pool.",
		},
		func() float64 {
			if activePool != nil {
				return float64(activePool.Stat().IdleConns())
			}
			return 0
		},
	)

	dbPoolTotalConns = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "paysplit",
			Subsystem: "db",
			Name:      "pool_total_conns",
			Help:      "Total number of connections in the PostgreSQL pool.",
		},
		func() float64 {
			if activePool != nil {
				return float64(activePool.Stat().TotalConns())
			}
			return 0
		},
	)

	DBListenerConnected = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "paysplit",
			Subsystem: "db",
			Name:      "listener_connected",
			Help:      "Whether the shared PostgreSQL notification listener is connected and subscribed to every channel.",
		},
	)

	DBListenerReconnectsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "db",
			Name:      "listener_reconnects_total",
			Help:      "Total shared PostgreSQL notification listener reconnects by bounded reason.",
		},
		[]string{"reason"},
	)

	DBListenerInvalidPayloadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "db",
			Name:      "listener_invalid_payloads_total",
			Help:      "Total invalid PostgreSQL notification payloads by bounded channel.",
		},
		[]string{"channel"},
	)

	// Domain Metrics Module 3: Bill & OCR v1 (Spec 3 index.md:187, AC-14)
	OCRQueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "paysplit",
			Subsystem: "ocr",
			Name:      "queue_depth",
			Help:      "Current number of queued OCR jobs.",
		},
	)

	OCRProviderDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "paysplit",
			Subsystem: "ocr",
			Name:      "provider_duration_seconds",
			Help:      "Histogram of OCR provider extraction latency in seconds.",
			Buckets:   []float64{0.5, 1, 2, 3, 5, 8, 10, 15, 20},
		},
		[]string{"provider", "outcome"},
	)

	OCRJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "ocr",
			Name:      "jobs_total",
			Help:      "Total number of OCR jobs processed, labeled by final application state and error code.",
		},
		[]string{"state", "error_code"},
	)

	OCRStaleApplyTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "ocr",
			Name:      "stale_apply_total",
			Help:      "Total number of stale candidate apply rejections.",
		},
		[]string{"reason"},
	)

	BillMismatchBlockTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "bill",
			Name:      "mismatch_block_total",
			Help:      "Total number of review/finalize attempts blocked by mismatch.",
		},
		[]string{"reason"},
	)

	MediaCleanupFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "media",
			Name:      "cleanup_failures_total",
			Help:      "Total number of media cleanup job failures.",
		},
		[]string{"reason"},
	)

	BillFinalizeDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "paysplit",
			Subsystem: "bill",
			Name:      "finalize_duration_seconds",
			Help:      "Histogram of bill finalize duration in seconds.",
			Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
		},
		[]string{"outcome"},
	)

	BillPreviewDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "paysplit",
			Subsystem: "bill",
			Name:      "preview_duration_seconds",
			Help:      "Histogram of bill preview calculation duration in seconds.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1},
		},
		[]string{"outcome"},
	)
	SettlementOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "paysplit", Subsystem: "settlement", Name: "operations_total", Help: "Settlement operations by operation and outcome."}, []string{"operation", "outcome"})
	SettlementWorkerRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "paysplit", Subsystem: "settlement", Name: "worker_runs_total", Help: "Settlement background worker runs by kind and outcome."}, []string{"kind", "outcome"})

	// Group Bill Close v1 (Spec 0008 Observability 1 đến 4)
	GroupBillSubmissionLocksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "group_bill",
			Name:      "submission_locks_total",
			Help:      "Total group bill submission lock attempts by outcome.",
		},
		[]string{"outcome"},
	)
	GroupBillBulkBatchesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "group_bill",
			Name:      "bulk_batches_total",
			Help:      "Total bulk finalize batches completed by outcome.",
		},
		[]string{"outcome"},
	)
	GroupBillBulkItemsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "group_bill",
			Name:      "bulk_items_total",
			Help:      "Total bulk finalize items processed by outcome.",
		},
		[]string{"outcome"},
	)
	GroupBillBulkDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "paysplit",
			Subsystem: "group_bill",
			Name:      "bulk_duration_seconds",
			Help:      "Histogram of bulk finalize batch duration from creation to completion in seconds.",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300},
		},
		[]string{"outcome"},
	)

	UserSSEConnectionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "user_sse",
			Name:      "connections_total",
			Help:      "Total user SSE subscribe attempts by bounded result.",
		},
		[]string{"result"},
	)
	UserSSEActiveConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "paysplit",
			Subsystem: "user_sse",
			Name:      "active_connections",
			Help:      "Local active user SSE streams.",
		},
	)
	UserSSEClosesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "user_sse",
			Name:      "closes_total",
			Help:      "Total user SSE closes by public reason.",
		},
		[]string{"reason"},
	)
	UserEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "user",
			Name:      "events_total",
			Help:      "Total user realtime events by bounded channel and kind.",
		},
		[]string{"channel", "kind"},
	)
	UserEventInvalidPayloadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "user",
			Name:      "event_invalid_payloads_total",
			Help:      "Total invalid user realtime payloads by bounded channel and reason.",
		},
		[]string{"channel", "reason"},
	)
	LegacySSERequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "paysplit",
			Subsystem: "legacy_sse",
			Name:      "requests_total",
			Help:      "Total legacy SSE requests by route and app version class.",
		},
		[]string{"route", "app_version_class"},
	)
)

var activePool *pgxpool.Pool

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(dbPoolAcquiredConns)
	prometheus.MustRegister(dbPoolIdleConns)
	prometheus.MustRegister(dbPoolTotalConns)
	prometheus.MustRegister(DBListenerConnected)
	prometheus.MustRegister(DBListenerReconnectsTotal)
	prometheus.MustRegister(DBListenerInvalidPayloadsTotal)

	// Register Module 3 Domain Metrics
	prometheus.MustRegister(OCRQueueDepth)
	prometheus.MustRegister(OCRProviderDuration)
	prometheus.MustRegister(OCRJobsTotal)
	prometheus.MustRegister(OCRStaleApplyTotal)
	prometheus.MustRegister(BillMismatchBlockTotal)
	prometheus.MustRegister(MediaCleanupFailuresTotal)
	prometheus.MustRegister(BillFinalizeDuration)
	prometheus.MustRegister(BillPreviewDuration)
	prometheus.MustRegister(SettlementOperationsTotal)
	prometheus.MustRegister(SettlementWorkerRunsTotal)
	prometheus.MustRegister(GroupBillSubmissionLocksTotal)
	prometheus.MustRegister(GroupBillBulkBatchesTotal)
	prometheus.MustRegister(GroupBillBulkItemsTotal)
	prometheus.MustRegister(GroupBillBulkDuration)
	prometheus.MustRegister(UserSSEConnectionsTotal)
	prometheus.MustRegister(UserSSEActiveConnections)
	prometheus.MustRegister(UserSSEClosesTotal)
	prometheus.MustRegister(UserEventsTotal)
	prometheus.MustRegister(UserEventInvalidPayloadsTotal)
	prometheus.MustRegister(LegacySSERequestsTotal)
}

func RecordSettlementOperation(operation, outcome string) {
	SettlementOperationsTotal.WithLabelValues(operation, outcome).Inc()
}
func RecordSettlementWorkerRun(kind, outcome string) {
	SettlementWorkerRunsTotal.WithLabelValues(kind, outcome).Inc()
}

// RecordGroupBillSubmissionLock ghi nhận một lần khóa gửi hóa đơn (Spec 0008 Observability 1).
func RecordGroupBillSubmissionLock(outcome string, _ time.Duration) {
	GroupBillSubmissionLocksTotal.WithLabelValues(outcome).Inc()
}

// RecordGroupBillBulkBatch ghi nhận một batch chốt toàn bộ hoàn tất (Observability 2).
func RecordGroupBillBulkBatch(outcome string) {
	GroupBillBulkBatchesTotal.WithLabelValues(outcome).Inc()
}

// RecordGroupBillBulkItem ghi nhận kết quả xử lý một item batch (Observability 3).
func RecordGroupBillBulkItem(outcome string) {
	GroupBillBulkItemsTotal.WithLabelValues(outcome).Inc()
}

// ObserveGroupBillBulkDuration đo thời gian từ lúc tạo batch đến khi hoàn tất (Observability 4).
func ObserveGroupBillBulkDuration(outcome string, duration time.Duration) {
	GroupBillBulkDuration.WithLabelValues(outcome).Observe(duration.Seconds())
}

// Helper methods ghi nhận metrics cho Module 3
func RecordOCRJob(state, errorCode string) {
	OCRJobsTotal.WithLabelValues(state, errorCode).Inc()
}

func RecordOCRProviderDuration(provider, outcome string, duration time.Duration) {
	OCRProviderDuration.WithLabelValues(provider, outcome).Observe(duration.Seconds())
}

func RecordOCRStaleApply(reason string) {
	OCRStaleApplyTotal.WithLabelValues(reason).Inc()
}

func RecordBillMismatchBlock(reason string) {
	BillMismatchBlockTotal.WithLabelValues(reason).Inc()
}

func RecordMediaCleanupFailure(reason string) {
	MediaCleanupFailuresTotal.WithLabelValues(reason).Inc()
}

func RecordBillFinalize(outcome string, duration time.Duration) {
	BillFinalizeDuration.WithLabelValues(outcome).Observe(duration.Seconds())
}

func RecordBillPreview(outcome string, duration time.Duration) {
	BillPreviewDuration.WithLabelValues(outcome).Observe(duration.Seconds())
}

// SetOCRQueueDepth ghi nhận số lượng job OCR đang queued/processing, được một poller định kỳ đọc
// trực tiếp từ database (nguồn sự thật), thay vì cộng/trừ trong tiến trình, để chính xác qua
// restart, rollback giao dịch và nhiều replica (Spec 3 AC-14).
func SetOCRQueueDepth(depth float64) {
	OCRQueueDepth.Set(depth)
}

// RegisterDBPool đăng ký pool database để đo lường metrics kết nối.
func RegisterDBPool(pool *pgxpool.Pool) {
	activePool = pool
}

func SetDBListenerConnected(connected bool) {
	if connected {
		DBListenerConnected.Set(1)
		return
	}
	DBListenerConnected.Set(0)
}

func RecordDBListenerReconnect(reason string) {
	switch reason {
	case "acquire", "listen", "wait":
		DBListenerReconnectsTotal.WithLabelValues(reason).Inc()
	}
}

func RecordDBListenerInvalidPayload(channel string) {
	switch channel {
	case "bill_events", "group_events", "user_events":
		DBListenerInvalidPayloadsTotal.WithLabelValues(channel).Inc()
	}
}

func RecordUserSSEConnection(result string) {
	switch result {
	case "opened", "auth_failed", "rate_limited", "listener_unavailable", "streaming_unsupported", "replace_publish_failed":
		UserSSEConnectionsTotal.WithLabelValues(result).Inc()
	}
}

func SetUserSSEActiveConnections(delta float64) {
	UserSSEActiveConnections.Add(delta)
}

func RecordUserSSEClose(reason string) {
	switch reason {
	case "max_connection_age", "session_ended", "replaced", "listener_reset", "backpressure":
		UserSSEClosesTotal.WithLabelValues(reason).Inc()
	}
}

func RecordUserEvent(channel, kind string) {
	switch channel {
	case "group_events", "bill_events", "user_events":
	default:
		return
	}
	switch kind {
	case "roster", "invalidate", "ocr_updated", "stream_replace", "session_ended", "ready", "heartbeat", "close":
		UserEventsTotal.WithLabelValues(channel, kind).Inc()
	}
}

func RecordUserEventInvalidPayload(channel, reason string) {
	switch channel {
	case "group_events", "bill_events", "user_events":
	default:
		return
	}
	switch reason {
	case "invalid_json", "unknown_schema", "unknown_kind", "missing_recipient", "conflicting_recipient", "invalid_uuid", "invalid_body", "oversized":
		UserEventInvalidPayloadsTotal.WithLabelValues(channel, reason).Inc()
	}
}

func RecordLegacySSERequest(route, appVersionClass string) {
	switch route {
	case "group", "bill":
	default:
		return
	}
	switch appVersionClass {
	case "supported", "legacy", "unknown":
		LegacySSERequestsTotal.WithLabelValues(route, appVersionClass).Inc()
	}
}

// HTTPMetricsMiddleware ghi nhận counter và latency cho mỗi request.
func HTTPMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chiMiddleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		duration := time.Since(start).Seconds()

		routePattern := chi.RouteContext(r.Context()).RoutePattern()
		if routePattern == "" {
			routePattern = "unmatched"
		}

		statusCode := strconv.Itoa(ww.Status())
		httpRequestsTotal.WithLabelValues(r.Method, routePattern, statusCode).Inc()
		httpRequestDuration.WithLabelValues(r.Method, routePattern).Observe(duration)
	})
}

// MetricsHandler trả về handler chuẩn của Prometheus có kiểm tra cấu hình bật/tắt và token bí mật.
func MetricsHandler(enabled bool, bearerToken string) http.Handler {
	promHandler := promhttp.Handler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled {
			_ = helpers.WriteAPIError(w, http.StatusNotFound, "NOT_FOUND", "metrics endpoint disabled", nil)
			return
		}

		if strings.TrimSpace(bearerToken) != "" {
			authHeader := r.Header.Get("Authorization")
			parts := strings.Fields(authHeader)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] != bearerToken {
				_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "invalid or missing metrics bearer token", nil)
				return
			}
		}

		promHandler.ServeHTTP(w, r)
	})
}

// DBPingChecker kiểm tra kết nối tới cơ sở dữ liệu PostgreSQL phục vụ readiness probe.
type DBPingChecker interface {
	Ping(ctx context.Context) error
}
