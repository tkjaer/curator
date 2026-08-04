package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Options configure an rsync deployment.
type Options struct {
	Target      string
	Delete      bool
	DryRun      bool
	ShowCommand bool
	Stdout      io.Writer
	Stderr      io.Writer
}

// Rsync copies the contents of outputDir to the configured destination.
func Rsync(ctx context.Context, outputDir string, opts Options) error {
	if strings.TrimSpace(opts.Target) == "" {
		return errors.New("publish: rsync destination is required")
	}
	if _, err := os.Stat(filepath.Join(outputDir, "index.html")); err != nil {
		return fmt.Errorf("publish: %s/index.html not found; run a build first", outputDir)
	}

	rsync, err := exec.LookPath("rsync")
	if err != nil {
		return errors.New("publish: rsync not found in PATH")
	}

	args := rsyncArgs(outputDir, opts)

	if opts.ShowCommand && opts.Stdout != nil {
		fmt.Fprintln(opts.Stdout, "running: rsync", strings.Join(args, " "))
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, rsync, args...)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = &stderr
	if opts.Stderr != nil {
		cmd.Stderr = io.MultiWriter(opts.Stderr, &stderr)
	}
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("publish: rsync failed: %w: %s", err, message)
		}
		return fmt.Errorf("publish: rsync failed: %w", err)
	}
	return nil
}

func rsyncArgs(outputDir string, opts Options) []string {
	args := []string{"-a"}
	if opts.Delete {
		args = append(args, "--delete")
	}
	if opts.DryRun {
		args = append(args, "--dry-run", "--itemize-changes")
	}
	args = append(args, "--", filepath.Clean(outputDir)+string(os.PathSeparator), opts.Target)
	return args
}
