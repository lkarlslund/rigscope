package run

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/lkarlslund/rigscope/internal/collectors"
	"github.com/lkarlslund/rigscope/internal/events"
)

type Config struct {
	Name               string
	Command            []string
	OutputDir          string
	Interval           time.Duration
	Collectors         []collectors.Collector
	RecordingInitially bool
	Echo               bool
}

type jsonlWriter struct {
	mu sync.Mutex
	f  *os.File
}

func newJSONL(path string) (*jsonlWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &jsonlWriter{f: f}, nil
}

func (w *jsonlWriter) Write(v map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.f.Write(append(data, '\n')); err != nil {
		return err
	}
	return w.f.Sync()
}

func (w *jsonlWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}

type session struct {
	cfg       Config
	recording bool
	mu        sync.RWMutex
	events    *jsonlWriter
	samples   *jsonlWriter
	stdout    *os.File
	stderr    *os.File
}

func Workload(ctx context.Context, cfg Config) (int, error) {
	if cfg.Interval <= 0 {
		return 0, fmt.Errorf("interval must be positive")
	}
	if len(cfg.Command) == 0 {
		return 0, fmt.Errorf("missing workload command")
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return 0, err
	}

	s, err := newSession(cfg)
	if err != nil {
		return 0, err
	}
	defer s.close()

	if err := s.writeMeta(); err != nil {
		return 0, err
	}

	cmd := exec.CommandContext(ctx, cfg.Command[0], cfg.Command[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = stdoutR.Close()
	}()
	defer func() {
		_ = stdoutW.Close()
	}()

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = stderrR.Close()
	}()
	defer func() {
		_ = stderrW.Close()
	}()

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	_ = stdoutW.Close()
	_ = stderrW.Close()

	_ = s.writeEvent(map[string]any{
		"type":    "process_start",
		"pid":     cmd.Process.Pid,
		"command": cfg.Command,
	})

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		s.sampleLoop(runCtx)
	}()
	go func() {
		defer wg.Done()
		s.stdoutLoop(stdoutR)
	}()
	go func() {
		defer wg.Done()
		s.stderrLoop(stderrR)
	}()

	err = cmd.Wait()
	cancel()
	wg.Wait()

	code := exitCode(err)
	_ = s.writeEvent(map[string]any{
		"type":       "process_exit",
		"returncode": code,
	})
	if err != nil && code == 0 {
		return 0, err
	}
	return code, nil
}

func newSession(cfg Config) (*session, error) {
	eventsFile, err := newJSONL(filepath.Join(cfg.OutputDir, "events.jsonl"))
	if err != nil {
		return nil, err
	}
	samplesFile, err := newJSONL(filepath.Join(cfg.OutputDir, "samples.jsonl"))
	if err != nil {
		_ = eventsFile.Close()
		return nil, err
	}
	stdout, err := os.OpenFile(filepath.Join(cfg.OutputDir, "stdout.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		_ = eventsFile.Close()
		_ = samplesFile.Close()
		return nil, err
	}
	stderr, err := os.OpenFile(filepath.Join(cfg.OutputDir, "stderr.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		_ = eventsFile.Close()
		_ = samplesFile.Close()
		_ = stdout.Close()
		return nil, err
	}
	return &session{
		cfg:       cfg,
		recording: cfg.RecordingInitially,
		events:    eventsFile,
		samples:   samplesFile,
		stdout:    stdout,
		stderr:    stderr,
	}, nil
}

func (s *session) close() {
	_ = s.events.Close()
	_ = s.samples.Close()
	_ = s.stdout.Close()
	_ = s.stderr.Close()
}

func (s *session) writeMeta() error {
	names := make([]string, 0, len(s.cfg.Collectors))
	for _, c := range s.cfg.Collectors {
		names = append(names, c.Name())
	}
	meta := map[string]any{
		"name":        s.cfg.Name,
		"command":     s.cfg.Command,
		"started_utc": time.Now().UTC().Format(time.RFC3339Nano),
		"interval_ms": float64(s.cfg.Interval) / float64(time.Millisecond),
		"collectors":  names,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.cfg.OutputDir, "meta.json"), data, 0o644)
}

func (s *session) sampleLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		if s.isRecording() {
			_ = s.samples.Write(collectors.SampleAll(ctx, s.cfg.Collectors))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *session) stdoutLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(s.stdout, line)
		if s.cfg.Echo {
			fmt.Println(line)
		}
		parsed := events.ParseLine(line)
		if parsed.Err != nil {
			_ = s.writeEvent(map[string]any{
				"type":  "parse_error",
				"error": parsed.Err.Error(),
				"raw":   parsed.Raw,
			})
			continue
		}
		if parsed.Event != nil {
			s.handleEvent(parsed.Event, parsed.Raw)
		}
	}
}

func (s *session) stderrLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(s.stderr, line)
		if s.cfg.Echo {
			fmt.Fprintln(os.Stderr, line)
		}
	}
}

func (s *session) handleEvent(event map[string]any, raw string) {
	if typ, _ := event["type"].(string); typ == "recording" {
		switch action, _ := event["action"].(string); action {
		case "start":
			s.setRecording(true)
		case "stop":
			s.setRecording(false)
		default:
			event["parse_warning"] = "recording event requires action=start|stop"
		}
	} else if typ != "point" && typ != "mark" {
		event["parse_warning"] = "unknown event type"
	}
	event["raw"] = raw
	_ = s.writeEvent(event)
}

func (s *session) writeEvent(event map[string]any) error {
	event["t_wall"] = float64(time.Now().UnixNano()) / 1e9
	event["t_unix_nano"] = time.Now().UnixNano()
	event["utc"] = time.Now().UTC().Format(time.RFC3339Nano)
	return s.events.Write(event)
}

func (s *session) isRecording() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recording
}

func (s *session) setRecording(recording bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recording = recording
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}
