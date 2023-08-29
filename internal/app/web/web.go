package web

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// App is the web application.
type App struct {
	logger *slog.Logger
	srv    *http.Server
	port   string
}

// NewApp creates a new web app.
func NewApp(logger *slog.Logger, port string, router chi.Router, h *Handlers) *App {
	router.Route("/", func(r chi.Router) {
		r.Post("/upload", h.createSubtitles)
		r.Get("/subtitles", h.listSubtitles)
		r.Get("/subtitles/{name}", h.subtitleFile)
		r.Get("/subtitles/zip", h.subtitlesZip)
		r.Delete("/subtitles/{name}", h.deleteSubtitle)
	})

	return &App{
		logger: logger,
		srv: &http.Server{
			Addr:    net.JoinHostPort("", port),
			Handler: router,
		},
		port: port,
	}
}

// Run starts the web server.
func (s *App) Run() error {
	s.logger.Info("Starting web app")

	go func() {
		if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
			s.logger.Error("Could not listen and server", slog.String("error", err.Error()))
		}
	}()
	return nil
}

// Stop stops the web server.
func (app *App) Stop() error {
	app.logger.Info("Stopping web app")

	if err := app.srv.Shutdown(context.TODO()); err != nil {
		return fmt.Errorf("could not shutdown server: %w", err)
	}
	return nil
}
