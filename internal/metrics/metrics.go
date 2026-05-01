package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	PaymentsTotal       *prometheus.CounterVec
	PaymentsRetried     prometheus.Counter
	ProviderCalls       *prometheus.CounterVec
	QueueMessages       *prometheus.CounterVec
	SweeperResets       prometheus.Counter
	ProcessingDuration  prometheus.Histogram
	ProviderDuration    prometheus.Histogram
	ActiveProcessing    prometheus.Gauge
	PendingRetry        prometheus.Gauge
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration prometheus.Histogram
}

func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	return &Metrics{
		PaymentsTotal: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "dinero_payments_total",
			Help: "Total payments by final status",
		}, []string{"status"}),

		PaymentsRetried: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Name: "dinero_payments_retried_total",
			Help: "Total payment retry attempts",
		}),

		ProviderCalls: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "dinero_provider_calls_total",
			Help: "Provider calls by result",
		}, []string{"result"}),

		QueueMessages: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "dinero_queue_messages_total",
			Help: "Queue messages by operation and result",
		}, []string{"operation", "result"}),

		SweeperResets: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Name: "dinero_sweeper_resets_total",
			Help: "Stale processing payments reset by sweeper",
		}),

		ProcessingDuration: promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
			Name:    "dinero_payment_processing_duration_seconds",
			Help:    "Time from queue receive to final status",
			Buckets: prometheus.DefBuckets,
		}),

		ProviderDuration: promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
			Name:    "dinero_provider_call_duration_seconds",
			Help:    "Provider API call latency",
			Buckets: prometheus.DefBuckets,
		}),

		ActiveProcessing: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Name: "dinero_payments_processing_active",
			Help: "Currently in processing state",
		}),

		PendingRetry: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Name: "dinero_payments_pending_retry",
			Help: "Failed payments awaiting retry",
		}),

		HTTPRequestsTotal: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "dinero_http_requests_total",
			Help: "Total HTTP requests by method, route pattern, and status",
		}, []string{"method", "path", "status"}),

		HTTPRequestDuration: promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
			Name:    "dinero_http_request_duration_seconds",
			Help:    "HTTP request latency by method and route pattern",
			Buckets: prometheus.DefBuckets,
		}),
	}
}

// HTTPMiddleware instruments every HTTP request with Prometheus metrics.
// It should be applied after chi routes are mounted so RoutePattern() is accurate.
func (m *Metrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m == nil {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		path := chi.RouteContext(r.Context()).RoutePattern()
		if path == "" {
			path = r.URL.Path
		}

		m.HTTPRequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(ww.Status())).Inc()
		m.HTTPRequestDuration.Observe(time.Since(start).Seconds())
	})
}

func HandlerFor(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
