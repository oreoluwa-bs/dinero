package server

import (
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/oreoluwa-bs/dinero/internal/provider"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

type Server struct {
	paymentProvider provider.Provider
	store           repository.Queries
}

func NewServer(prov provider.Provider, store repository.Queries) *Server {
	return &Server{
		paymentProvider: prov,
		store:           store,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/charges", func(r chi.Router) {
		r.Post("/", s.createCharge)
	})
	return r
}
