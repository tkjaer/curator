package imaging

import (
	"image"
	"image/color"
	"testing"

	"github.com/tkjaer/curator/internal/model"
)

func solid(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

func TestFitWidthScalesDown(t *testing.T) {
	got := Fit(solid(2000, 1000), model.Preset{Kind: "width", MaxWidth: 800})
	w, h := Size(got)
	if w != 800 || h != 400 {
		t.Errorf("got %dx%d, want 800x400", w, h)
	}
}

func TestFitWidthNoUpscale(t *testing.T) {
	got := Fit(solid(500, 250), model.Preset{Kind: "width", MaxWidth: 800})
	if w, h := Size(got); w != 500 || h != 250 {
		t.Errorf("got %dx%d, want 500x250 (no upscale)", w, h)
	}
}

func TestFitCoverCropsToExact(t *testing.T) {
	got := Fit(solid(2000, 1000), model.Preset{Kind: "cover", MaxWidth: 400, MaxHeight: 400})
	if w, h := Size(got); w != 400 || h != 400 {
		t.Errorf("got %dx%d, want 400x400", w, h)
	}
}
