package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/alesr/videoscriber/internal/pkg/subtitles"
	"github.com/go-chi/chi/v5"
)

const (
	subtitlesDir string = "subtitles"
	maxFileSize  int64  = 1 << 30 // 1GB
)

type subtitler interface {
	GenerateFromAudioData(ctx context.Context, inputs []*subtitles.Input) error
}

type Handlers struct {
	logger    *slog.Logger
	subtitler subtitler
}

func NewHandlers(logger *slog.Logger, subtitler subtitler) *Handlers {
	return &Handlers{
		logger:    logger,
		subtitler: subtitler,
	}
}

type uploadResponse struct {
	Filenames []string `json:"filenames"`
}

func (h *Handlers) createSubtitles(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxFileSize); err != nil {
		h.e(w, "Failed to parse the request", err, http.StatusBadRequest)
		return
	}

	files, ok := r.MultipartForm.File["file"]
	if !ok || len(files) == 0 {
		h.e(w, "No file part in request", nil, http.StatusBadRequest)
		return
	}

	genSubtitleInput := make([]*subtitles.Input, 0, len(files))

	for _, header := range files {
		uploadedFile, err := header.Open()
		if err != nil {
			h.e(w, "Failed to open the uploaded file", err, http.StatusInternalServerError)
			return
		}
		defer uploadedFile.Close()

		genSubtitleInput = append(genSubtitleInput, &subtitles.Input{
			Data:     uploadedFile,
			FileName: header.Filename,
			Language: "pt", // hardcoded for now
		})
	}

	if err := h.subtitler.GenerateFromAudioData(r.Context(), genSubtitleInput); err != nil {
		h.e(w, "Failed to generate subtitles", err, http.StatusInternalServerError)
		return
	}

	// Add these lines to send a JSON response back to the Electron app
	response := map[string]string{
		"message": "Subtitles generated successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

type listSubtitlesResponse struct {
	Subtitles []string `json:"subtitles"`
}

func (h *Handlers) listSubtitles(w http.ResponseWriter, r *http.Request) {
	var listResp listSubtitlesResponse

	if err := filepath.WalkDir(subtitlesDir, func(filePath string, file os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("could not walk in the directory: %w", err)
		}

		if file.IsDir() {
			return nil
		}

		if filepath.Ext(file.Name()) != ".srt" {
			return nil
		}

		name := filepath.Base(filePath)

		listResp.Subtitles = append(listResp.Subtitles, name)

		return nil
	}); err != nil {
		h.e(w, "Failed to compile zip file", err, http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(listResp); err != nil {
		h.e(w, "Failed to encode response", err, http.StatusInternalServerError)
		return
	}
}

func (h *Handlers) subtitleFile(w http.ResponseWriter, r *http.Request) {
	subName := chi.URLParam(r, "name")

	if err := filepath.WalkDir(subtitlesDir, func(filePath string, file os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("could not walk in the directory: %w", err)
		}

		if file.Name() == subName {
			w.Header().Set("Content-Type", "application/x-subrip")
			w.Header().Set("Content-Disposition", "attachment; filename="+subName)
			http.ServeFile(w, r, filePath)
		}
		return nil
	}); err != nil {
		h.e(w, "Failed to compile zip file", err, http.StatusInternalServerError)
	}
}

func (h *Handlers) deleteSubtitle(w http.ResponseWriter, r *http.Request) {
	subName := chi.URLParam(r, "name")

	if err := filepath.WalkDir(subtitlesDir, func(filePath string, file os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("could not walk in the directory: %w", err)
		}

		if file.Name() == subName {
			if err := os.Remove(filePath); err != nil {
				return fmt.Errorf("could not remove subtitle: %w", err)
			}
		}
		return nil
	}); err != nil {
		h.e(w, "Failed to compile zip file", err, http.StatusInternalServerError)
	}
}

func (h *Handlers) subtitlesZip(w http.ResponseWriter, r *http.Request) {
	buffer := bytes.NewBuffer(nil)

	zipWritter := zip.NewWriter(buffer)
	defer zipWritter.Close()

	if err := filepath.WalkDir(subtitlesDir, func(filePath string, file os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("could not walk in the directory: %w", err)
		}

		if file.IsDir() {
			return nil
		}

		if filepath.Ext(file.Name()) != ".srt" {
			return nil
		}

		name := filepath.Base(filePath)

		zipEntry, err := zipWritter.Create(name)
		if err != nil {
			return fmt.Errorf("could not create zip entry: %w", err)
		}

		data, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("could not open file: %w", err)
		}

		if _, err := io.Copy(zipEntry, data); err != nil {
			return fmt.Errorf("could not copy data: %w", err)
		}
		return nil
	}); err != nil {
		h.e(w, "Failed to compile zip file", err, http.StatusInternalServerError)
	}

	if err := zipWritter.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=legendas.zip")

	w.Write(buffer.Bytes())
}

func (h *Handlers) e(w http.ResponseWriter, message string, err error, statusCode int) {
	if err != nil {
		h.logger.Error("Responding with error", slog.String("error", err.Error()))
	} else {
		h.logger.Error("Responding with error", slog.String("message", message))
	}
	http.Error(w, message, statusCode)
}
