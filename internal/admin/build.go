package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/tkjaer/curator/internal/build"
)

// buildStatus tracks an in-progress or finished build for the admin UI.
type buildStatus struct {
	mu         sync.Mutex
	running    bool
	everRun    bool
	progress   build.Progress
	report     build.Report
	err        string
	finishedAt time.Time
}

// begin marks a build as started, returning false if one is already running.
func (b *buildStatus) begin() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return false
	}
	b.running = true
	b.everRun = true
	b.progress = build.Progress{}
	b.report = build.Report{}
	b.err = ""
	return true
}

func (b *buildStatus) setProgress(p build.Progress) {
	b.mu.Lock()
	b.progress = p
	b.mu.Unlock()
}

func (b *buildStatus) finish(report build.Report, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running = false
	b.report = report
	b.finishedAt = time.Now()
	if err != nil {
		b.err = err.Error()
	}
}

type buildStatusJSON struct {
	Running    bool   `json:"running"`
	EverRun    bool   `json:"everRun"`
	Stage      string `json:"stage"`
	Done       int    `json:"done"`
	Total      int    `json:"total"`
	Error      string `json:"error"`
	Galleries  int    `json:"galleries"`
	Photos     int    `json:"photos"`
	Generated  int    `json:"generated"`
	Reused     int    `json:"reused"`
	DurationMs int64  `json:"durationMs"`
}

func (b *buildStatus) snapshot() buildStatusJSON {
	b.mu.Lock()
	defer b.mu.Unlock()
	return buildStatusJSON{
		Running:    b.running,
		EverRun:    b.everRun,
		Stage:      b.progress.Stage,
		Done:       b.progress.Done,
		Total:      b.progress.Total,
		Error:      b.err,
		Galleries:  b.report.Galleries,
		Photos:     b.report.Photos,
		Generated:  b.report.Generated,
		Reused:     b.report.Reused,
		DurationMs: b.report.Duration.Milliseconds(),
	}
}

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	if s.build == nil {
		s.redirect(w, r, s.link(), "Build is not available")
		return
	}
	if !s.builds.begin() {
		s.redirect(w, r, s.link(), "A build is already running")
		return
	}
	go func() {
		report, err := s.build(context.Background(), s.builds.setProgress)
		s.builds.finish(report, err)
	}()
	s.redirect(w, r, s.link(), "Build started")
}

func (s *Server) handleBuildStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.builds.snapshot())
}
