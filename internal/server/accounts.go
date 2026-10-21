package server

import (
	"context"

	"github.com/go-chi/chi/v5"
)

// accountRoutes: external CardDAV / Nextcloud accounts (docs/design.md).
func (s *Server) accountRoutes(r chi.Router) {}

// syncLoop: pulls and pushes remote address books.
func (s *Server) syncLoop(ctx context.Context) { <-ctx.Done() }
