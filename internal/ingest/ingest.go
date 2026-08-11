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

// Rescan re-reads dimensions and EXIF metadata from every item's original file
// and rewrites the stored fields. It returns how many items were updated and
// how many were skipped because their original could not be read. It does not
// add or remove items.
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
		originalPath := filepath.Join(cfg.OriginalsDir(), it.OriginalPath)
		width, height, err := imaging.Dimensions(originalPath)
		if err != nil {
			skipped++
			continue
		}
		meta, err := exif.Extract(originalPath)
		if err != nil {
			skipped++
			continue
		}
		it.Width = width
		it.Height = height
		it.Aspect = model.ClassifyAspect(width, height)
		it.EXIF = meta.Raw
		it.EmbeddedCamera = meta.Camera
		it.Camera = ResolveCamera(it.EmbeddedCamera, it.ManualCamera)
		it.EmbeddedLens = meta.Lens
		it.SidecarLens = meta.SidecarLens
		it.XMPLens = meta.XMPLens
		it.Lens = policy.Resolve(it.Camera, meta.Lens, it.LightroomLens, meta.SidecarLens, meta.XMPLens, it.ManualLens)
		it.Aperture = meta.Aperture
		it.Shutter = meta.Shutter
		it.ISO = meta.ISO
		it.Focal = meta.Focal
		it.TakenAt = meta.TakenAt
		if err := st.UpdateItemEXIF(ctx, it); err != nil {
			return updated, skipped, err
		}
		if err := st.FillItemTextMetadata(ctx, it.ID, meta.Title, meta.Description); err != nil {
			return updated, skipped, err
		}
		if err := st.ReplaceItemImportedTags(ctx, it.ID, store.TagSourceMetadata, meta.Keywords); err != nil {
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
	_, err := ImportUploadIDWithSidecar(ctx, st, cfg, galleryID, gallerySlug, filename, r, sidecar)
	return err
}

// ImportUploadIDWithSidecar imports an image and returns its item id.
func ImportUploadIDWithSidecar(ctx context.Context, st *store.Store, cfg config.Config, galleryID int64, gallerySlug, filename string, r, sidecar io.Reader) (int64, error) {
	name := filepath.Base(filename)
	exists, err := st.ItemFilenameExists(ctx, galleryID, name)
	if err != nil {
		return 0, err
	}
	if exists {
		return 0, fmt.Errorf("%s is already in this gallery", name)
	}
	return ImportUploadIDWithSidecarAt(ctx, st, cfg, galleryID, gallerySlug, filename, filename, r, sidecar)
}

// ImportUploadIDWithSidecarAt imports an image using a distinct storage name
// while retaining filename as its user-facing name.
func ImportUploadIDWithSidecarAt(ctx context.Context, st *store.Store, cfg config.Config, galleryID int64, galleryPath, filename, storageName string, r, sidecar io.Reader) (int64, error) {
	item, keywords, err := importUploadItem(ctx, st, cfg, galleryID, galleryPath, filename, storageName, r, sidecar)
	if err != nil {
		return 0, err
	}
	itemID, err := st.CreateItem(ctx, item)
	if err != nil {
		return 0, err
	}
	if err := st.ReplaceItemImportedTags(ctx, itemID, store.TagSourceMetadata, keywords); err != nil {
		_ = st.DeleteItem(ctx, itemID)
		return 0, err
	}
	return itemID, nil
}

// ReplaceUploadWithSidecar replaces an item's source media while preserving
// its id, caption, ordering, and publication state.
func ReplaceUploadWithSidecar(ctx context.Context, st *store.Store, cfg config.Config, itemID, galleryID int64, gallerySlug, filename string, r, sidecar io.Reader) error {
	return ReplaceUploadWithSidecarAt(ctx, st, cfg, itemID, galleryID, gallerySlug, filename, filename, r, sidecar)
}

// ReplaceUploadWithSidecarAt replaces synchronized media at a stable storage
// name while retaining filename as its user-facing name.
func ReplaceUploadWithSidecarAt(ctx context.Context, st *store.Store, cfg config.Config, itemID, galleryID int64, galleryPath, filename, storageName string, r, sidecar io.Reader) error {
	oldItem, err := st.Item(ctx, itemID)
	if err != nil {
		return err
	}
	item, keywords, err := importUploadItem(ctx, st, cfg, galleryID, galleryPath, filename, storageName, r, sidecar)
	if err != nil {
		return err
	}
	item.ID = itemID
	item.LightroomLens = oldItem.LightroomLens
	item.ManualCamera = oldItem.ManualCamera
	item.ManualLens = oldItem.ManualLens
	settings, err := st.Settings(ctx)
	if err != nil {
		return err
	}
	policy, err := LensPolicyFromSettings(settings)
	if err != nil {
		return err
	}
	item.Camera = ResolveCamera(item.EmbeddedCamera, item.ManualCamera)
	item.Lens = policy.Resolve(item.Camera, item.EmbeddedLens, item.LightroomLens, item.SidecarLens, item.XMPLens, item.ManualLens)
	if err := st.ReplaceItemMedia(ctx, item); err != nil {
		return err
	}
	if err := st.FillItemTextMetadata(ctx, item.ID, item.Title, item.Description); err != nil {
		return err
	}
	if err := st.ReplaceItemImportedTags(ctx, item.ID, store.TagSourceMetadata, keywords); err != nil {
		return err
	}
	if oldItem.OriginalPath != item.OriginalPath {
		oldPath := filepath.Join(cfg.OriginalsDir(), filepath.FromSlash(oldItem.OriginalPath))
		_ = os.Remove(oldPath)
		_ = os.Remove(strings.TrimSuffix(oldPath, filepath.Ext(oldPath)) + ".xmp")
	}
	return nil
}

func importUploadItem(ctx context.Context, st *store.Store, cfg config.Config, galleryID int64, galleryPath, filename, storageName string, r, sidecar io.Reader) (model.Item, []string, error) {
	if !IsImage(filename) {
		return model.Item{}, nil, fmt.Errorf("unsupported file type: %s", filename)
	}

	destDir := filepath.Join(cfg.OriginalsDir(), filepath.FromSlash(galleryPath))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return model.Item{}, nil, err
	}
	name := filepath.Base(filename)
	storedName := filepath.Base(storageName)
	dest := filepath.Join(destDir, storedName)
	if err := writeFile(dest, r); err != nil {
		return model.Item{}, nil, fmt.Errorf("write %s: %w", storedName, err)
	}
	sidecarDest := strings.TrimSuffix(dest, filepath.Ext(dest)) + ".xmp"
	if sidecar != nil {
		if err := writeFile(sidecarDest, sidecar); err != nil {
			return model.Item{}, nil, fmt.Errorf("write %s sidecar: %w", name, err)
		}
	} else if err := os.Remove(sidecarDest); err != nil && !os.IsNotExist(err) {
		return model.Item{}, nil, fmt.Errorf("remove stale %s sidecar: %w", name, err)
	}

	w, h, err := imaging.Dimensions(dest)
	if err != nil {
		return model.Item{}, nil, fmt.Errorf("read %s: %w", name, err)
	}
	meta, err := exif.Extract(dest)
	if err != nil {
		return model.Item{}, nil, fmt.Errorf("exif %s: %w", name, err)
	}
	settings, err := st.Settings(ctx)
	if err != nil {
		return model.Item{}, nil, err
	}
	policy, err := LensPolicyFromSettings(settings)
	if err != nil {
		return model.Item{}, nil, err
	}

	return model.Item{
		GalleryID:      galleryID,
		OriginalPath:   filepath.ToSlash(filepath.Join(galleryPath, storedName)),
		Filename:       name,
		Width:          w,
		Height:         h,
		Aspect:         model.ClassifyAspect(w, h),
		Status:         model.ItemPublished,
		Title:          meta.Title,
		Description:    meta.Description,
		EXIF:           meta.Raw,
		Camera:         meta.Camera,
		EmbeddedCamera: meta.Camera,
		Lens:           policy.Lens(meta),
		EmbeddedLens:   meta.Lens,
		SidecarLens:    meta.SidecarLens,
		XMPLens:        meta.XMPLens,
		Aperture:       meta.Aperture,
		Shutter:        meta.Shutter,
		ISO:            meta.ISO,
		Focal:          meta.Focal,
		TakenAt:        meta.TakenAt,
	}, meta.Keywords, nil
}

// ResolveCamera chooses a manual camera name over the imported EXIF value.
func ResolveCamera(embeddedCamera, manualCamera string) string {
	if manualCamera != "" {
		return manualCamera
	}
	return embeddedCamera
}

// LensPolicy controls explicit lens overrides and metadata fallbacks.
type LensPolicy struct {
	UseXMPFallback bool
	Mappings       map[string]string
	NameMappings   map[string]string
}

// LensPolicyFromSettings builds and validates the lens metadata policy.
func LensPolicyFromSettings(settings map[string]string) (LensPolicy, error) {
	mappings, err := ParseLensMappings(settings["metadata.lens_mappings"])
	if err != nil {
		return LensPolicy{}, err
	}
	nameMappings, err := ParseLensNameMappings(settings["metadata.lens_name_mappings"])
	if err != nil {
		return LensPolicy{}, err
	}
	return LensPolicy{
		UseXMPFallback: settings["metadata.use_lightroom_lens_profile"] == "true",
		Mappings:       mappings,
		NameMappings:   nameMappings,
	}, nil
}

// ParseLensMappings parses one camera-to-lens mapping per line.
func ParseLensMappings(value string) (map[string]string, error) {
	return parseLensMappings(value, "Camera = Lens", false)
}

// ParseLensNameMappings parses one existing-to-canonical lens name per line.
func ParseLensNameMappings(value string) (map[string]string, error) {
	return parseLensMappings(value, "Existing lens = Canonical lens", true)
}

func parseLensMappings(value, format string, rejectDuplicates bool) (map[string]string, error) {
	mappings := map[string]string{}
	for lineNumber, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		from, to, ok := strings.Cut(line, "=")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !ok || from == "" || to == "" {
			return nil, fmt.Errorf("invalid lens mapping on line %d: use %s", lineNumber+1, format)
		}
		if _, exists := mappings[from]; exists && rejectDuplicates {
			return nil, fmt.Errorf("duplicate lens mapping for %q on line %d", from, lineNumber+1)
		}
		mappings[from] = to
	}
	return mappings, nil
}

// Lens resolves a stored lens using EXIF and configured metadata policies.
func (p LensPolicy) Lens(meta exif.Data) string {
	return p.Resolve(meta.Camera, meta.Lens, "", meta.SidecarLens, meta.XMPLens, "")
}

// Resolve chooses a lens from stored source values without rereading a photo.
func (p LensPolicy) Resolve(camera, embeddedLens, lightroomLens, sidecarLens, xmpLens, manualLens string) string {
	var lens string
	if manualLens != "" {
		lens = manualLens
	} else if lightroomLens != "" {
		lens = lightroomLens
	} else if embeddedLens != "" {
		lens = embeddedLens
	} else if sidecarLens != "" {
		lens = sidecarLens
	} else if mappedLens := p.Mappings[camera]; mappedLens != "" {
		lens = mappedLens
	} else if p.UseXMPFallback {
		lens = xmpLens
	}
	if canonical := p.NameMappings[lens]; canonical != "" {
		return canonical
	}
	return lens
}

func writeFile(dest string, r io.Reader) error {
	out, err := os.CreateTemp(filepath.Dir(dest), ".curator-upload-*")
	if err != nil {
		return err
	}
	tempPath := out.Name()
	defer os.Remove(tempPath)

	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	if err := out.Chmod(0o644); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, dest)
}
