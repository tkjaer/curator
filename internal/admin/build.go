package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkjaer/curator/internal/build"
	"github.com/tkjaer/curator/internal/ingest"
)

// buildStatus tracks an in-progress or finished build for the admin UI.
type buildStatus struct {
	mu         sync.Mutex
	instance   string
	id         uint64
	running    bool
	pending    bool
	everRun    bool
	progress   build.Progress
	report     build.Report
	err        string
	finishedAt time.Time
}

var buildInstanceSequence atomic.Uint64

func newBuildStatus() *buildStatus {
	return &buildStatus{instance: strconv.FormatInt(time.Now().UnixNano(), 36) + "-" +
		strconv.FormatUint(buildInstanceSequence.Add(1), 36)}
}

// begin marks a build as started, returning false if one is already running.
func (b *buildStatus) begin() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return false
	}
	b.startLocked()
	return true
}

func (b *buildStatus) queue() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		b.pending = true
		return false
	}
	b.startLocked()
	return true
}

func (b *buildStatus) startLocked() {
	b.id++
	b.running = true
	b.everRun = true
	b.progress = build.Progress{}
	b.report = build.Report{}
	b.err = ""
}

func (b *buildStatus) setProgress(p build.Progress) {
	b.mu.Lock()
	b.progress = p
	b.mu.Unlock()
}

func (b *buildStatus) finish(report build.Report, err error) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.report = report
	b.finishedAt = time.Now()
	if err != nil {
		b.err = err.Error()
	}
	if b.pending {
		b.pending = false
		b.startLocked()
		return true
	}
	b.running = false
	return false
}

type buildStatusJSON struct {
	BuildInstance string `json:"buildInstance"`
	BuildID       uint64 `json:"buildId"`
	Running       bool   `json:"running"`
	EverRun       bool   `json:"everRun"`
	Stage         string `json:"stage"`
	Done          int    `json:"done"`
	Total         int    `json:"total"`
	Error         string `json:"error"`
	Galleries     int    `json:"galleries"`
	Photos        int    `json:"photos"`
	Generated     int    `json:"generated"`
	Reused        int    `json:"reused"`
	FeedUpdated   bool   `json:"feedUpdated"`
	DurationMs    int64  `json:"durationMs"`
}

func (b *buildStatus) snapshot() buildStatusJSON {
	b.mu.Lock()
	defer b.mu.Unlock()
	return buildStatusJSON{
		BuildInstance: b.instance,
		BuildID:       b.id,
		Running:       b.running,
		EverRun:       b.everRun,
		Stage:         b.progress.Stage,
		Done:          b.progress.Done,
		Total:         b.progress.Total,
		Error:         b.err,
		Galleries:     b.report.Galleries,
		Photos:        b.report.Photos,
		Generated:     b.report.Generated,
		Reused:        b.report.Reused,
		FeedUpdated:   b.report.FeedUpdated,
		DurationMs:    b.report.Duration.Milliseconds(),
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
	go s.runBuildQueue()
	s.redirect(w, r, s.link(), "")
}

func (s *Server) queueBuild() error {
	if s.build == nil {
		return errors.New("build is not available")
	}
	if s.builds.queue() {
		go s.runBuildQueue()
	}
	return nil
}

func (s *Server) runBuildQueue() {
	for {
		report, err := s.build(context.Background(), s.builds.setProgress)
		if err == nil {
			err = s.deployAfterBuild(context.Background())
		}
		if !s.builds.finish(report, err) {
			return
		}
	}
}

func (s *Server) deployAfterBuild(ctx context.Context) error {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return err
	}
	if settings["publish.rsync_enabled"] != "true" {
		return nil
	}
	target := settings["publish.rsync_target"]
	if target == "" {
		return errors.New("rsync deployment is enabled but no destination is configured")
	}
	s.builds.setProgress(build.Progress{Stage: "Deploying"})
	return s.deploy(ctx, target, settings["publish.rsync_delete"] == "true")
}

func (s *Server) handleBuildStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.builds.snapshot())
}

// handleRescan re-reads EXIF metadata from every item's original file. It runs
// synchronously; the work is I/O-light (no image re-encoding).
func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	updated, skipped, err := ingest.Rescan(r.Context(), s.store, s.cfg)
	if err != nil {
		s.redirect(w, r, s.link(), "Rescan failed: "+err.Error())
		return
	}
	msg := fmt.Sprintf("Refreshed metadata for %d photo(s)", updated)
	if skipped > 0 {
		msg += fmt.Sprintf(", %d skipped", skipped)
	}
	s.redirect(w, r, s.link(), msg)
}
