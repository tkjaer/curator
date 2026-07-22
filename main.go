package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tkjaer/curator/internal/admin"
	"github.com/tkjaer/curator/internal/build"
	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/ingest"
	"github.com/tkjaer/curator/internal/model"
	slugpkg "github.com/tkjaer/curator/internal/slug"
	"github.com/tkjaer/curator/internal/store"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

const version = "0.1.0-dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "curator:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		return cmdInit(rest)
	case "import":
		return cmdImport(rest)
	case "rescan":
		return cmdRescan(rest)
	case "build":
		return cmdBuild(rest)
	case "publish":
		return cmdPublish(rest)
	case "serve":
		return cmdServe(rest)
	case "set-password":
		return cmdSetPassword(rest)
	case "version", "-version", "--version":
		fmt.Println("curator", version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q (try \"curator help\")", cmd)
	}
}

func usage() {
	fmt.Println(`curator - a photo gallery CMS

usage:
  curator init [-content DIR]                       create a new content root
  curator import -title T [-slug S] [-content DIR] SRC
                                                   import a folder of images
  curator rescan [-content DIR]                     re-read EXIF from originals
  curator build [-content DIR] [-output DIR]        render the static site
  curator publish -target DEST [-dry-run] [-delete] [-no-build] [-content DIR]
                                                   build then rsync to a server
  curator serve [-listen ADDR] [-base-path P] [-content DIR] [-output DIR]
                                                   run the admin web UI
  curator set-password [-content DIR]               set the admin login password
  curator version                                   print the version
  curator help                                      show this help`)
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	content := fs.String("content", ".", "content root directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := config.New(*content, "")
	if err := os.MkdirAll(cfg.OriginalsDir(), 0o755); err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(context.Background()); err != nil {
		return err
	}

	fmt.Println("initialized content root at", cfg.ContentRoot)
	return nil
}

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	content := fs.String("content", ".", "content root directory")
	title := fs.String("title", "", "gallery title")
	slug := fs.String("slug", "", "gallery slug (defaults from title)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *title == "" {
		return fmt.Errorf("import: -title is required")
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("import: expected a source directory")
	}
	srcDir := fs.Arg(0)

	slugValue := *slug
	if slugValue == "" {
		slugValue = slugpkg.Make(*title)
	}

	ctx := context.Background()
	cfg := config.New(*content, "")
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	galleryID, err := st.CreateGallery(ctx, model.Gallery{
		Slug:     slugValue,
		Title:    *title,
		Type:     model.GalleryGrid,
		Status:   model.GalleryPublished,
		SortMode: model.SortByDate,
	})
	if err != nil {
		return err
	}

	n, err := ingest.ImportDir(ctx, st, cfg, galleryID, slugValue, srcDir)
	if err != nil {
		return err
	}

	fmt.Printf("imported %d image(s) into gallery %q\n", n, slugValue)
	return nil
}

func cmdRescan(args []string) error {
	fs := flag.NewFlagSet("rescan", flag.ContinueOnError)
	content := fs.String("content", ".", "content root directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	cfg := config.New(*content, "")
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	updated, skipped, err := ingest.Rescan(ctx, st, cfg)
	if err != nil {
		return err
	}

	fmt.Printf("rescanned %d item(s)", updated)
	if skipped > 0 {
		fmt.Printf(", %d skipped", skipped)
	}
	fmt.Println()
	return nil
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	content := fs.String("content", ".", "content root directory")
	output := fs.String("output", "", "output directory (default: CONTENT/output)")
	quiet := fs.Bool("quiet", false, "suppress progress output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := config.New(*content, *output)
	onProgress := cliProgress
	if *quiet {
		onProgress = nil
	}
	report, err := buildSite(context.Background(), cfg, onProgress)
	if err != nil {
		return err
	}

	if !*quiet {
		fmt.Fprintln(os.Stderr) // finish the progress line
	}
	fmt.Printf("built %d galleries, %d photos (%d images generated, %d reused) in %s → %s\n",
		report.Galleries, report.Photos, report.Generated, report.Reused,
		report.Duration.Round(time.Millisecond), cfg.OutputDir)
	return nil
}

// cliProgress prints a single, overwriting progress line to stderr.
func cliProgress(p build.Progress) {
	if p.Total > 0 {
		fmt.Fprintf(os.Stderr, "\r\033[K%-10s %d/%d", p.Stage, p.Done, p.Total)
	} else {
		fmt.Fprintf(os.Stderr, "\r\033[K%-10s", p.Stage)
	}
}

// buildSite opens the store and renders the site to cfg.OutputDir.
func buildSite(ctx context.Context, cfg config.Config, onProgress func(build.Progress)) (build.Report, error) {
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return build.Report{}, err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return build.Report{}, err
	}
	th, err := loadSiteTheme(ctx, st)
	if err != nil {
		return build.Report{}, err
	}
	b := build.New(st, th, cfg)
	b.OnProgress = onProgress
	return b.BuildReport(ctx)
}

func cmdPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	content := fs.String("content", ".", "content root directory")
	output := fs.String("output", "", "output directory (default: CONTENT/output)")
	target := fs.String("target", "", "rsync destination, e.g. user@host:/srv/site")
	dryRun := fs.Bool("dry-run", false, "show what rsync would do without changing anything")
	del := fs.Bool("delete", false, "delete remote files that no longer exist locally")
	noBuild := fs.Bool("no-build", false, "skip the build and publish the existing output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" {
		return errors.New("publish: -target is required (e.g. user@host:/srv/site)")
	}

	ctx := context.Background()
	cfg := config.New(*content, *output)

	if !*noBuild {
		report, err := buildSite(ctx, cfg, cliProgress)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr)
		fmt.Printf("built %d galleries, %d photos in %s\n",
			report.Galleries, report.Photos, report.Duration.Round(time.Millisecond))
	}

	// Refuse to publish an empty or missing build, which with -delete could wipe
	// the remote site.
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "index.html")); err != nil {
		return fmt.Errorf("publish: %s/index.html not found; run a build first", cfg.OutputDir)
	}

	rsync, err := exec.LookPath("rsync")
	if err != nil {
		return errors.New("publish: rsync not found in PATH")
	}

	rsyncArgs := []string{"-a"}
	if *del {
		rsyncArgs = append(rsyncArgs, "--delete")
	}
	if *dryRun {
		rsyncArgs = append(rsyncArgs, "--dry-run", "-v")
	}
	// Trailing slash: copy the contents of output, not the directory itself.
	rsyncArgs = append(rsyncArgs, cfg.OutputDir+string(os.PathSeparator), *target)

	fmt.Println("running: rsync", strings.Join(rsyncArgs, " "))
	cmd := exec.Command(rsync, rsyncArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	if *dryRun {
		fmt.Println("dry run complete — no changes were made")
	} else {
		fmt.Println("published to", *target)
	}
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	content := fs.String("content", ".", "content root directory")
	output := fs.String("output", "", "output directory (default: CONTENT/output)")
	listen := fs.String("listen", "127.0.0.1:8080", "address to listen on")
	basePath := fs.String("base-path", "", "base path when served under a subpath (e.g. /admin)")
	trustProxy := fs.Bool("trust-proxy", false, "trust X-Forwarded-* headers from a reverse proxy")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	cfg := config.New(*content, *output)
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	runBuild := func(ctx context.Context, onProgress func(build.Progress)) (build.Report, error) {
		th, err := loadSiteTheme(ctx, st)
		if err != nil {
			return build.Report{}, err
		}
		b := build.New(st, th, cfg)
		b.OnProgress = onProgress
		return b.BuildReport(ctx)
	}

	srv, err := admin.New(st, cfg, admin.Options{
		BasePath:   *basePath,
		TrustProxy: *trustProxy,
		Build:      runBuild,
		Themes:     availableThemes(),
	})
	if err != nil {
		return err
	}

	root := *basePath
	if root == "" {
		root = "/"
	}
	fmt.Printf("curator admin listening on http://%s%s\n", *listen, root)
	return (&http.Server{Addr: *listen, Handler: srv.Handler()}).ListenAndServe()
}

func cmdSetPassword(args []string) error {
	fs := flag.NewFlagSet("set-password", flag.ContinueOnError)
	content := fs.String("content", ".", "content root directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	password, err := readPassword()
	if err != nil {
		return err
	}
	if password == "" {
		return errors.New("password must not be empty")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	ctx := context.Background()
	cfg := config.New(*content, "")
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	if err := st.SetSetting(ctx, "admin.password_hash", string(hash)); err != nil {
		return err
	}

	fmt.Println("admin password set")
	return nil
}

// readPassword reads a password from the terminal without echoing, or a single
// line from stdin when not attached to a terminal (for scripting).
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "New admin password: ")
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
