package server

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	"github.com/oreoluwa-bs/dinero/internal/metrics"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/queue"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

type Server struct {
	paymentProvider provider.Provider
	store           repository.Queries
	db              *sql.DB
	publisher       queue.Publisher
	logger          *slog.Logger
	registry        *prometheus.Registry
	metrics         *metrics.Metrics
	tracerProvider  trace.TracerProvider
	tracer          trace.Tracer
}

func NewServer(prov provider.Provider, store repository.Queries, db *sql.DB, publisher queue.Publisher, logger *slog.Logger, registry *prometheus.Registry, mtr *metrics.Metrics, tracerProvider trace.TracerProvider, tracer trace.Tracer) *Server {
	return &Server{
		paymentProvider: prov,
		store:           store,
		db:              db,
		publisher:       publisher,
		logger:          logger,
		registry:        registry,
		metrics:         mtr,
		tracerProvider:  tracerProvider,
		tracer:          tracer,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(otelhttp.NewMiddleware("dinero-api", otelhttp.WithTracerProvider(s.tracerProvider)))
	r.Use(s.metrics.HTTPMiddleware)

	r.Get("/metrics", metrics.HandlerFor(s.registry).ServeHTTP)
	r.Get("/health", s.health)
	r.Get("/ready", s.ready)

	r.Route("/charges", func(r chi.Router) {
		r.Post("/", s.createCharge)
		r.Get("/{reference}", s.getCharge)
	})
	return r
}
