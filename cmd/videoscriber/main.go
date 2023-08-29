package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"

	"github.com/alesr/audiostripper"
	"github.com/alesr/videoscriber/internal/app/web"
	"github.com/alesr/videoscriber/internal/pkg/subtitles"

	"github.com/alesr/whisperclient"
	"github.com/go-chi/chi/v5"
)

const (
	sampleRate     string = "3800"
	whisperAIModel string = "whisper-1"
	subtitlesDir   string = "subtitles"
	tmpDir         string = "tmp"
)

var extractCmd audiostripper.ExtractCmd = func(params *audiostripper.ExtractCmdParams) error {
	cmd := exec.Command(
		"ffmpeg", "-y", "-i", params.InputFile, "-vn", "-acodec", "pcm_s16le", "-ar", params.SampleRate,
		"-ac", "2", "-b:a", "32k", params.OutputFile,
	)

	cmd.Stderr = params.Stderr
	return cmd.Run()
}

func main() {
	// Configurations.

	port := flag.String("port", "8080", "port to listen")
	flag.Parse()

	logger := makeLogger(*port)

	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		logger.Error("OPENAI_API_KEY not set")
		os.Exit(1)
	}

	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		logger.Info("creating tmp directory for storing video and audio files", slog.String("dir", tmpDir))

		if err := os.Mkdir(tmpDir, os.ModePerm); err != nil {
			logger.Error("Could not create tmp dir", slog.String("error", err.Error()))
		}
	}

	if _, err := os.Stat(subtitlesDir); os.IsNotExist(err) {
		logger.Info("creating subtitles directory for storing subtitles", slog.String("dir", subtitlesDir))

		if err := os.Mkdir(subtitlesDir, os.ModePerm); err != nil {
			logger.Error("Could not create subtitles dir", slog.String("error", err.Error()))
		}
	}

	// Extracts audio from video.
	audioStripper := audiostripper.New(extractCmd)

	// Requests subtitles from OpenAI.
	whisperAIClient := whisperclient.New(&http.Client{}, openAIKey, whisperAIModel)

	// Coordinate audio extraction and subtitles request in concurrent manner.
	subtitler, err := subtitles.New(
		logger,
		sampleRate,
		subtitlesDir,
		tmpDir,
		audioStripper,
		whisperAIClient,
	)
	if err != nil {
		logger.Error("Could not initialize subtitles", slog.String("error", err.Error()))
		os.Exit(2)
	}

	// Handles requests.
	handlers := web.NewHandlers(logger, subtitler)

	// Starts web app.

	webApp := web.NewApp(logger, *port, chi.NewRouter(), handlers)

	if err := webApp.Run(); err != nil {
		logger.Error("Could not start rest app", slog.String("error", err.Error()))
	}

	// Handles OS signals.

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	defer signal.Stop(c)

	<-c

	if err := webApp.Stop(); err != nil {
		logger.Error("Could not stop rest app", slog.String("error", err.Error()))
	}
}

func makeLogger(port string) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}).WithAttrs(func() []slog.Attr {
		var attributes = []slog.Attr{
			{
				Key:   "port",
				Value: slog.StringValue(port),
			},
		}
		return attributes
	}()))
}
