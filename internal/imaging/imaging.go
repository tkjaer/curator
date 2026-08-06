// Package imaging decodes source photos and produces scaled derivatives. It is
// pure Go (no libvips), decoding JPEG and PNG and scaling with a high-quality
// resampler.
package imaging

import (
	"image"
	"image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"

	"github.com/tkjaer/curator/internal/model"
)

// Image is an alias for the standard library image type, so callers need not
// import both packages.
type Image = image.Image

// Dimensions returns the display width and height after applying EXIF
// orientation, without decoding the whole image.
func Dimensions(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	orientation := exifOrientation(f)
	if _, err := f.Seek(0, 0); err != nil {
		return 0, 0, err
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	if orientation >= 5 && orientation <= 8 {
		return cfg.Height, cfg.Width, nil
	}
	return cfg.Width, cfg.Height, nil
}

// Load decodes the image at path and applies its EXIF orientation.
func Load(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	orientation := exifOrientation(f)
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return applyOrientation(img, orientation), nil
}

func exifOrientation(f *os.File) int {
	x, err := exif.Decode(f)
	if err != nil {
		return 1
	}
	tag, err := x.Get(exif.Orientation)
	if err != nil {
		return 1
	}
	orientation, err := tag.Int(0)
	if err != nil || orientation < 1 || orientation > 8 {
		return 1
	}
	return orientation
}

func applyOrientation(img image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return img
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	dstWidth, dstHeight := width, height
	if orientation >= 5 {
		dstWidth, dstHeight = height, width
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var dstX, dstY int
			switch orientation {
			case 2:
				dstX, dstY = width-1-x, y
			case 3:
				dstX, dstY = width-1-x, height-1-y
			case 4:
				dstX, dstY = x, height-1-y
			case 5:
				dstX, dstY = y, x
			case 6:
				dstX, dstY = height-1-y, x
			case 7:
				dstX, dstY = height-1-y, width-1-x
			case 8:
				dstX, dstY = y, width-1-x
			}
			dst.Set(dstX, dstY, img.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return dst
}

// Fit scales img according to preset, never upscaling for width presets. Cover
// presets scale and center-crop to exact dimensions (used for thumbnails).
func Fit(img image.Image, p model.Preset) image.Image {
	if p.Kind == "cover" {
		return coverCrop(img, p.MaxWidth, p.MaxHeight)
	}

	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if p.MaxWidth <= 0 || sw <= p.MaxWidth {
		return img
	}
	tw := p.MaxWidth
	th := int(math.Round(float64(sh) * float64(tw) / float64(sw)))
	return scale(img, tw, th)
}

func coverCrop(img image.Image, w, h int) image.Image {
	if w <= 0 || h <= 0 {
		return img
	}
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()

	ratio := math.Max(float64(w)/float64(sw), float64(h)/float64(sh))
	tw := int(math.Round(float64(sw) * ratio))
	th := int(math.Round(float64(sh) * ratio))
	scaled := scale(img, tw, th)

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	offset := image.Pt((tw-w)/2, (th-h)/2)
	draw.Draw(dst, dst.Bounds(), scaled, scaled.Bounds().Min.Add(offset), draw.Src)
	return dst
}

func scale(img image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	return dst
}

// SaveJPEG writes img to path as JPEG, creating parent directories as needed.
func SaveJPEG(path string, img image.Image, quality int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: quality}); err != nil {
		return err
	}
	return f.Close()
}

// Size reports an image's width and height.
func Size(img image.Image) (int, int) {
	b := img.Bounds()
	return b.Dx(), b.Dy()
}
