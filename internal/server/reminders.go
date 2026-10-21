package server

import (
	"context"

	"github.com/go-chi/chi/v5"
)

// reminderLoop: birthday reminders (docs/design.md).
func (s *Server) reminderLoop(ctx context.Context) { <-ctx.Done() }

// seedDemo: demo data for DEMO_DATA=true.
func (s *Server) seedDemo(ctx context.Context) error { return nil }

// feedRoutes: GET /feed/birthdays/{token}.ics (docs/api.md, Birthdays).
func (s *Server) feedRoutes(r chi.Router) {}
