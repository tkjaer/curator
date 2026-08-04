package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tkjaer/curator/internal/imaging"
	"github.com/tkjaer/curator/internal/model"
	"github.com/tkjaer/curator/internal/render"
	"github.com/tkjaer/curator/internal/slug"
)

// galleryPhotos loads a gallery's published items, generates their derivatives,
// and returns the photo views used for layout and rendering (as an ordered
// slice and keyed by item id). Protected galleries keep their derivatives under
// their own (auth-guarded) path, and their photos are never added to public
// facet indexes.
func (b *Builder) galleryPhotos(ctx context.Context, g model.Gallery, presets []model.Preset) ([]render.PhotoView, map[int64]render.PhotoView, error) {
	items, err := b.Store.ItemsByGallery(ctx, g.ID)
	if err != nil {
		return nil, nil, err
	}

	imgPrefix := generatedRoot + "/img"
	if g.Status == model.GalleryProtected {
		imgPrefix = strings.TrimSuffix(b.galleryRel(g.ID), "/") + "/img"
	}

	var views []render.PhotoView
	byItem := make(map[int64]render.PhotoView, len(items))
	for _, it := range items {
		if it.Status != model.ItemPublished {
			continue
		}
		it.Lens = b.lensPolicy.Resolve(it.Camera, it.EmbeddedLens, it.LightroomLens, it.SidecarLens, it.XMPLens, it.ManualLens)
		if !g.ShowTitle.Resolve(b.settings["site.default_gallery_show_title"] != "false") {
			it.Title = ""
		}
		if !g.ShowDescription.Resolve(b.settings["site.default_gallery_show_description"] != "false") {
			it.Description = ""
		}
		pv, err := b.derive(ctx, it, presets, imgPrefix)
		if err != nil {
			return nil, nil, err
		}
		if g.ShowEXIF.Resolve(b.settings["site.default_gallery_show_exif"] == "true") {
			pv.Exif = exifView(it)
		}
		if g.Status == model.GalleryPublished {
			b.accumulate(it, pv)
		}
		views = append(views, pv)
		byItem[it.ID] = pv

		b.processed++
		b.report.Photos++
		b.progress("images", b.processed, b.totalItems)
	}
	return views, byItem, nil
}

// derive generates every preset for an item (skipping ones already on disk) and
// assembles its PhotoView with a srcset. imgPrefix is the site-relative
// directory the derivatives are written under.
func (b *Builder) derive(ctx context.Context, it model.Item, presets []model.Preset, imgPrefix string) (render.PhotoView, error) {
	origPath := filepath.Join(b.Cfg.OriginalsDir(), it.OriginalPath)
	fileHash, err := hashFile(origPath)
	if err != nil {
		return render.PhotoView{}, err
	}

	pv := render.PhotoView{
		Slug:        slug.Make(strings.TrimSuffix(it.Filename, filepath.Ext(it.Filename))),
		Title:       it.Title,
		Description: it.Description,
		Width:       it.Width,
		Height:      it.Height,
		Aspect:      string(it.Aspect),
		Highlighted: it.Highlighted,
		Caption:     it.Caption,
		Alt:         altText(it),
	}

	var source lazyImage // lazily loaded
	for _, p := range presets {
		hash := deriveHash(fileHash, p.Name)
		rel := imgPrefix + "/" + hash + ".jpg"
		outPath := filepath.Join(b.Cfg.OutputDir, filepath.FromSlash(rel))
		url := b.site.BaseURL + "/" + rel

		var w, h int
		if fileExists(outPath) {
			w, h, err = imaging.Dimensions(outPath)
			if err != nil {
				return render.PhotoView{}, err
			}
			b.report.Reused++
		} else {
			if source.img == nil {
				if source.img, err = imaging.Load(origPath); err != nil {
					return render.PhotoView{}, err
				}
			}
			fitted := imaging.Fit(source.img, p)
			w, h = imaging.Size(fitted)
			if err := imaging.SaveJPEG(outPath, fitted, p.Quality); err != nil {
				return render.PhotoView{}, err
			}
			b.report.Generated++
		}

		if err := b.Store.UpsertDerivative(ctx, model.Derivative{
			ItemID: it.ID, Preset: p.Name, Width: w, Height: h, Path: rel, Hash: hash,
		}); err != nil {
			return render.PhotoView{}, err
		}
		b.keep(outPath)

		src := render.Source{URL: url, Width: w, Height: h}
		switch {
		case p.Name == "thumb":
			pv.Thumb = src
		case p.Name == "display":
			pv.Display = src
		case p.Kind == "width":
			pv.Srcset = append(pv.Srcset, src)
		}
	}

	if pv.Display.URL == "" && len(pv.Srcset) > 0 {
		pv.Display = pv.Srcset[len(pv.Srcset)-1]
	}
	pv.Zoom = pv.Display
	for _, src := range pv.Srcset {
		if src.Width > pv.Zoom.Width {
			pv.Zoom = src
		}
	}
	return pv, nil
}

// lazyImage holds a lazily decoded source so presets that are already on disk
// avoid decoding the original at all.
type lazyImage struct{ img imaging.Image }

func altText(it model.Item) string {
	if it.Title != "" {
		return it.Title
	}
	if it.Caption != "" {
		return it.Caption
	}
	if it.Description != "" {
		return it.Description
	}
	return strings.TrimSuffix(it.Filename, filepath.Ext(it.Filename))
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func deriveHash(fileHash, preset string) string {
	sum := sha256.Sum256([]byte(fileHash + ":" + preset))
	return hex.EncodeToString(sum[:])[:16]
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
