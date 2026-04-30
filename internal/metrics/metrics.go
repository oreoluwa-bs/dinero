package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

type Metrics struct {
	PaymentsTotal      *prometheus.CounterVec
	PaymentsRetried    prometheus.Counter
	ProviderCalls      *prometheus.CounterVec
	QueueMessages      *prometheus.CounterVec
	SweeperResets      prometheus.Counter
	ProcessingDuration prometheus.Histogram
	ProviderDuration   prometheus.Histogram
	ActiveProcessing   prometheus.Gauge
	PendingRetry       prometheus.Gauge
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
	}
}

func HandlerFor(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
