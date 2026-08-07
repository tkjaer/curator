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
	mu          sync.Mutex
	instance    string
	id          uint64
	running     bool
	pending     bool
	everRun     bool
	progress    build.Progress
	report      build.Report
	err         string
	rsyncTarget string
	rsyncStatus string
	finishedAt  time.Time
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
	b.rsyncTarget = ""
	b.rsyncStatus = ""
}

func (b *buildStatus) setProgress(p build.Progress) {
	b.mu.Lock()
	b.progress = p
	b.mu.Unlock()
}

func (b *buildStatus) startRsync(target string) {
	b.mu.Lock()
	b.progress = build.Progress{Stage: "Rsync"}
	b.rsyncTarget = target
	b.rsyncStatus = "running"
	b.mu.Unlock()
}

func (b *buildStatus) finish(report build.Report, err error) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.report = report
	b.finishedAt = time.Now()
	if b.rsyncStatus == "running" {
		b.rsyncStatus = "complete"
		if err != nil {
			b.rsyncStatus = "failed"
		}
	}
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
	Pending       bool   `json:"pending"`
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
	Unchanged     bool   `json:"unchanged"`
	DurationMs    int64  `json:"durationMs"`
	RsyncTarget   string `json:"rsyncTarget"`
	RsyncStatus   string `json:"rsyncStatus"`
	LastPublished string `json:"lastPublished"`
}

func (b *buildStatus) snapshot() buildStatusJSON {
	b.mu.Lock()
	defer b.mu.Unlock()
	return buildStatusJSON{
		BuildInstance: b.instance,
		BuildID:       b.id,
		Running:       b.running,
		Pending:       b.pending,
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
		Unchanged:     b.report.Unchanged,
		DurationMs:    b.report.Duration.Milliseconds(),
		RsyncTarget:   b.rsyncTarget,
		RsyncStatus:   b.rsyncStatus,
	}
}

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	if s.build == nil {
		s.redirect(w, r, s.link(), "Build is not available")
		return
	}
	if s.builds.queue() {
		go s.runBuildQueue()
		s.redirect(w, r, s.link(), "")
		return
	}
	s.redirect(w, r, s.link(), "Publish queued")
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
		if err == nil {
			err = s.store.SetSetting(context.Background(), "publish.last_success_at", time.Now().UTC().Format(time.RFC3339))
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
	s.builds.startRsync(target)
	return s.deploy(ctx, target, settings["publish.rsync_delete"] == "true")
}

func (s *Server) handleBuildStatus(w http.ResponseWriter, r *http.Request) {
	status := s.builds.snapshot()
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status.LastPublished = settings["publish.last_success_at"]
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
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
