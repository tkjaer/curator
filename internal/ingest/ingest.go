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
		sourcePath := filepath.Join(srcDir, e.Name())
		f, err := os.Open(sourcePath)
		if err != nil {
			return count, err
		}
		var sidecar *os.File
		if sidecarPath := exif.SidecarPath(sourcePath); sidecarPath != "" {
			sidecar, err = os.Open(sidecarPath)
			if err != nil {
				f.Close()
				return count, err
			}
		}
		err = ImportUploadWithSidecar(ctx, st, cfg, galleryID, gallerySlug, e.Name(), f, sidecar)
		f.Close()
		if sidecar != nil {
			sidecar.Close()
		}
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
	settings, err := st.Settings(ctx)
	if err != nil {
		return 0, 0, err
	}
	policy, err := LensPolicyFromSettings(settings)
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
		it.EmbeddedLens = meta.Lens
		it.SidecarLens = meta.SidecarLens
		it.XMPLens = meta.XMPLens
		it.Lens = policy.Lens(meta)
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
	return ImportUploadWithSidecar(ctx, st, cfg, galleryID, gallerySlug, filename, r, nil)
}

// ImportUploadWithSidecar imports an image and an optional standard XMP
// sidecar. Sidecars are stored beside originals using the basename form.
func ImportUploadWithSidecar(ctx context.Context, st *store.Store, cfg config.Config, galleryID int64, gallerySlug, filename string, r, sidecar io.Reader) error {
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
	if sidecar != nil {
		sidecarDest := strings.TrimSuffix(dest, filepath.Ext(dest)) + ".xmp"
		if err := writeFile(sidecarDest, sidecar); err != nil {
			return fmt.Errorf("write %s sidecar: %w", name, err)
		}
	}

	w, h, err := imaging.Dimensions(dest)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	meta, err := exif.Extract(dest)
	if err != nil {
		return fmt.Errorf("exif %s: %w", name, err)
	}
	settings, err := st.Settings(ctx)
	if err != nil {
		return err
	}
	policy, err := LensPolicyFromSettings(settings)
	if err != nil {
		return err
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
		Lens:         policy.Lens(meta),
		EmbeddedLens: meta.Lens,
		SidecarLens:  meta.SidecarLens,
		XMPLens:      meta.XMPLens,
		Aperture:     meta.Aperture,
		Shutter:      meta.Shutter,
		ISO:          meta.ISO,
		Focal:        meta.Focal,
		TakenAt:      meta.TakenAt,
	})
	return err
}

// LensPolicy controls fallbacks used when a photo has no embedded EXIF lens.
type LensPolicy struct {
	UseLightroom bool
	Mappings     map[string]string
}

// LensPolicyFromSettings builds and validates the lens metadata policy.
func LensPolicyFromSettings(settings map[string]string) (LensPolicy, error) {
	mappings, err := ParseLensMappings(settings["metadata.lens_mappings"])
	if err != nil {
		return LensPolicy{}, err
	}
	return LensPolicy{
		UseLightroom: settings["metadata.use_lightroom_lens_profile"] == "true",
		Mappings:     mappings,
	}, nil
}

// ParseLensMappings parses one camera-to-lens mapping per line.
func ParseLensMappings(value string) (map[string]string, error) {
	mappings := map[string]string{}
	for lineNumber, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		camera, lens, ok := strings.Cut(line, "=")
		camera, lens = strings.TrimSpace(camera), strings.TrimSpace(lens)
		if !ok || camera == "" || lens == "" {
			return nil, fmt.Errorf("invalid lens mapping on line %d: use Camera = Lens", lineNumber+1)
		}
		mappings[camera] = lens
	}
	return mappings, nil
}

// Lens resolves a stored lens using EXIF, configured mappings, then Lightroom.
func (p LensPolicy) Lens(meta exif.Data) string {
	return p.Resolve(meta.Camera, meta.Lens, meta.SidecarLens, meta.XMPLens)
}

// Resolve chooses a lens from stored source values without rereading a photo.
func (p LensPolicy) Resolve(camera, embeddedLens, sidecarLens, xmpLens string) string {
	if embeddedLens != "" {
		return embeddedLens
	}
	if sidecarLens != "" {
		return sidecarLens
	}
	if lens := p.Mappings[camera]; lens != "" {
		return lens
	}
	if p.UseLightroom {
		return xmpLens
	}
	return ""
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
