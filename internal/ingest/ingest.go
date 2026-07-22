// Package ingest imports source images into a gallery: it copies files into the
// content root's originals directory and records an item for each, capturing
// pixel dimensions and aspect classification.
package ingest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tkjaer/curator/internal/config"
	"github.com/tkjaer/curator/internal/exif"
	"github.com/tkjaer/curator/internal/imaging"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/store"
)

var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true}

// IsImage reports whether a filename has a supported image extension.
func IsImage(name string) bool {
	return imageExts[strings.ToLower(filepath.Ext(name))]
}

// ImportDir copies every supported image in srcDir into the gallery's originals
// folder and records it as an item. It returns the number of images imported.
func ImportDir(ctx context.Context, st *store.Store, cfg config.Config, galleryID int64, gallerySlug, srcDir string) (int, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !IsImage(e.Name()) {
			continue
		}
		f, err := os.Open(filepath.Join(srcDir, e.Name()))
		if err != nil {
			return count, err
		}
		err = ImportUpload(ctx, st, cfg, galleryID, gallerySlug, e.Name(), f)
		f.Close()
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// Rescan re-reads EXIF metadata from every item's original file and rewrites
// the stored fields. It returns how many items were updated and how many were
// skipped because their original could not be read. It does not add or remove
// items.
func Rescan(ctx context.Context, st *store.Store, cfg config.Config) (updated, skipped int, err error) {
	items, err := st.AllItems(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, it := range items {
		meta, err := exif.Extract(filepath.Join(cfg.OriginalsDir(), it.OriginalPath))
		if err != nil {
			skipped++
			continue
		}
		it.EXIF = meta.Raw
		it.Camera = meta.Camera
		it.Lens = meta.Lens
		it.Aperture = meta.Aperture
		it.Shutter = meta.Shutter
		it.ISO = meta.ISO
		it.Focal = meta.Focal
		it.TakenAt = meta.TakenAt
		if err := st.UpdateItemEXIF(ctx, it); err != nil {
			return updated, skipped, err
		}
		updated++
	}
	return updated, skipped, nil
}

// ImportUpload writes a single uploaded image into the gallery's originals
// folder and records it as an item.
func ImportUpload(ctx context.Context, st *store.Store, cfg config.Config, galleryID int64, gallerySlug, filename string, r io.Reader) error {
	if !IsImage(filename) {
		return fmt.Errorf("unsupported file type: %s", filename)
	}

	destDir := filepath.Join(cfg.OriginalsDir(), gallerySlug)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	name := filepath.Base(filename)
	dest := filepath.Join(destDir, name)
	if err := writeFile(dest, r); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	w, h, err := imaging.Dimensions(dest)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	meta, err := exif.Extract(dest)
	if err != nil {
		return fmt.Errorf("exif %s: %w", name, err)
	}

	_, err = st.CreateItem(ctx, model.Item{
		GalleryID:    galleryID,
		OriginalPath: filepath.Join(gallerySlug, name),
		Filename:     name,
		Width:        w,
		Height:       h,
		Aspect:       model.ClassifyAspect(w, h),
		Status:       model.ItemPublished,
		EXIF:         meta.Raw,
		Camera:       meta.Camera,
		Lens:         meta.Lens,
		Aperture:     meta.Aperture,
		Shutter:      meta.Shutter,
		ISO:          meta.ISO,
		Focal:        meta.Focal,
		TakenAt:      meta.TakenAt,
	})
	return err
}

func writeFile(dest string, r io.Reader) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, r); err != nil {
		return err
	}
	return out.Close()
}
