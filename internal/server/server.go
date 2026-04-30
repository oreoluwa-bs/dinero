package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/oreoluwa-bs/dinero/internal/metrics"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/queue"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

type Server struct {
	paymentProvider provider.Provider
	store           repository.Queries
	publisher       queue.Publisher
	logger          *slog.Logger
	registry        *prometheus.Registry
	metrics         *metrics.Metrics
}

func NewServer(prov provider.Provider, store repository.Queries, publisher queue.Publisher, logger *slog.Logger, registry *prometheus.Registry, mtr *metrics.Metrics) *Server {
	return &Server{
		paymentProvider: prov,
		store:           store,
		publisher:       publisher,
		logger:          logger,
		registry:        registry,
		metrics:         mtr,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/metrics", metrics.HandlerFor(s.registry).ServeHTTP)

	r.Route("/charges", func(r chi.Router) {
		r.Post("/", s.createCharge)
		r.Get("/{reference}", s.getCharge)
	})
	return r
}
