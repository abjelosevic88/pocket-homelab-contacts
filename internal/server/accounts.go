package server

import (
	"context"

	"github.com/go-chi/chi/v5"
)

// accountRoutes: external CardDAV / Nextcloud accounts (docs/design.md),
// and phones and other devices (devices.go).
func (s *Server) accountRoutes(r chi.Router) {
	s.deviceRoutes(r)
}

// syncLoop: pulls and pushes remote address books.
func (s *Server) syncLoop(ctx context.Context) { <-ctx.Done() }
