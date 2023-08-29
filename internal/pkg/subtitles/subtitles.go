package subtitles

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"log/slog"

	"github.com/alesr/audiostripper"
	"github.com/alesr/whisperclient"
)

type audioStripper interface {
	ExtractAudio(ctx context.Context, in *audiostripper.ExtractAudioInput) (*audiostripper.ExtractAudioOutput, error)
}

type whisperClient interface {
	TranscribeAudio(ctx context.Context, in whisperclient.TranscribeAudioInput) ([]byte, error)
}

// Input represents the input to the subtitle generator.
type Input struct {
	FileName string
	Data     io.Reader
	Language string // For now, we have the transcription language hardcoded to Portuguese.
}

// Subtitler is the subtitle generator.
type Subtitler struct {
	logger        *slog.Logger
	sampleRate    string
	outputDir     string
	tmpDir        string
	audioStripper audioStripper
	whisperClient whisperClient
}

// New returns a new subtitle generator.
func New(
	logger *slog.Logger,
	sampleRate, outputDir, tmpDir string,
	stripper audioStripper,
	whisperCli whisperClient,
) (*Subtitler, error) {
	return &Subtitler{
		logger:        logger,
		sampleRate:    sampleRate,
		outputDir:     outputDir,
		tmpDir:        tmpDir,
		audioStripper: stripper,
		whisperClient: whisperCli,
	}, nil
}

// GenerateFromAudioData generates subtitle from audio data.
func (s *Subtitler) GenerateFromAudioData(ctx context.Context, inputs []*Input) error {
	var (
		wg    sync.WaitGroup
		errCh = make(chan error, len(inputs))
	)

	for _, in := range inputs {
		wg.Add(1)

		go func(ctx context.Context, in *Input, errCh chan error) {
			defer wg.Done()
			s.processFile(ctx, in, errCh)
		}(ctx, in, errCh)
	}

	wg.Wait()

	close(errCh)

	// Wrap all errors in one.
	var err error
	for e := range errCh {
		err = fmt.Errorf("%w", e)
	}

	if err != nil {
		return fmt.Errorf("error while processing files: %w", err)
	}
	return nil
}

func (s *Subtitler) processFile(ctx context.Context, in *Input, errCh chan error) {
	videoPath, err := s.createVideoFile(in.FileName, in.Data)
	if err != nil {
		errCh <- fmt.Errorf("could not create video file: %w", err)
		return
	}
	defer s.removeFile(videoPath)

	audioFilePath, err := s.extractAudio(ctx, videoPath, s.sampleRate)
	if err != nil {
		errCh <- fmt.Errorf("could not extract audio: %w", err)
		return
	}
	defer s.removeFile(audioFilePath)

	audioData, err := readFile(audioFilePath)
	if err != nil {
		errCh <- fmt.Errorf("could not read audio file: %w", err)
		return
	}

	subData, err := s.requestSubtitle(ctx, audioData, in.FileName, s.sampleRate)
	if err != nil {
		errCh <- fmt.Errorf("could not generate subtitle: %w", err)
		return
	}

	subPath := subtitlePath(s.outputDir, in.FileName)

	if err := writeFile(subPath, subData); err != nil {
		errCh <- fmt.Errorf("could not write subtitle file: %w", err)
		return
	}
}

// createVideoFile creates a temporary video file and returns its path.
// The file is deleted after when the caller finishes.
func (s *Subtitler) createVideoFile(name string, data io.Reader) (string, error) {
	videoFile, err := os.CreateTemp(s.tmpDir, name)
	if err != nil {
		return "", fmt.Errorf("could not create video file: %w", err)
	}

	s.logger.Debug("Created video file", slog.String("filepath", videoFile.Name()))

	defer func() {
		if err := videoFile.Close(); err != nil {
			s.logger.Error("Could not close video file", slog.String("filepath", videoFile.Name()), slog.String("error", err.Error()))
		}
	}()

	if _, err := io.Copy(videoFile, data); err != nil {
		return "", fmt.Errorf("could not write video file: %w", err)
	}
	return videoFile.Name(), nil
}

// extractAudio extracts the audio from the video file.
// The audio file (.wav) is created in the same directory as the video file (tmp).
// The file is deleted after when the caller finishes.
func (s *Subtitler) extractAudio(ctx context.Context, filepath, sampleRate string) (string, error) {
	res, err := s.audioStripper.ExtractAudio(ctx, &audiostripper.ExtractAudioInput{
		SampleRate: sampleRate,
		FilePath:   filepath,
	})
	if err != nil {
		return "", fmt.Errorf("could not extract audio: %w", err)
	}
	return res.FilePath, nil
}

// requestSubtitle calls the Whisper API to generate subtitles for the given audio data.
func (s *Subtitler) requestSubtitle(ctx context.Context, audioData []byte, fileName, sampleRate string) ([]byte, error) {
	subtitleData, err := s.whisperClient.TranscribeAudio(ctx, whisperclient.TranscribeAudioInput{
		Name:     fileName,
		Language: whisperclient.LanguagePortuguese, // TODO: extend support for other languages.
		Format:   whisperclient.FormatSrt,
		Data:     bytes.NewReader(audioData),
	})
	if err != nil {
		return nil, fmt.Errorf("could not generate subtitle: %w", err)
	}
	return subtitleData, nil
}

func (s *Subtitler) removeFile(filePath string) {
	if err := os.Remove(filePath); err != nil {
		s.logger.Error("Could not remove file", slog.String("filepath", filePath), slog.String("error", err.Error()))
	}
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open file: %w", err)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("could not read file: %w", err)
	}

	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("could not close file: %w", err)
	}
	return data, nil
}

func writeFile(path string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not create file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("could not write file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("could not close file: %w", err)
	}
	return nil
}

func subtitlePath(dir, name string) string {
	return path.Join(dir, strings.Replace(name, path.Ext(name), ".srt", 1))
}
