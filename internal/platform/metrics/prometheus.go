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
)

var activePool *pgxpool.Pool

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(dbPoolAcquiredConns)
	prometheus.MustRegister(dbPoolIdleConns)
	prometheus.MustRegister(dbPoolTotalConns)

	// Register Module 3 Domain Metrics
	prometheus.MustRegister(OCRQueueDepth)
	prometheus.MustRegister(OCRProviderDuration)
	prometheus.MustRegister(OCRJobsTotal)
	prometheus.MustRegister(OCRStaleApplyTotal)
	prometheus.MustRegister(BillMismatchBlockTotal)
	prometheus.MustRegister(MediaCleanupFailuresTotal)
	prometheus.MustRegister(BillFinalizeDuration)
	prometheus.MustRegister(BillPreviewDuration)
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

func SetOCRQueueDepth(depth float64) {
	OCRQueueDepth.Set(depth)
}

// RegisterDBPool đăng ký pool database để đo lường metrics kết nối.
func RegisterDBPool(pool *pgxpool.Pool) {
	activePool = pool
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
