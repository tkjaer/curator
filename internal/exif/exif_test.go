package exif

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractNoEXIF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.Set(0, 0, color.RGBA{1, 2, 3, 255})
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	f.Close()

	d, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract returned error for EXIF-less file: %v", err)
	}
	if d.Camera != "" || d.TakenAt != nil {
		t.Errorf("expected empty Data, got %+v", d)
	}
}

func TestCameraNormalization(t *testing.T) {
	cases := []struct {
		make, model, want string
	}{
		{"Canon", "EOS R5", "Canon EOS R5"},
		{"NIKON CORPORATION", "NIKON D850", "NIKON D850"},
		{"FUJIFILM", "X-T5", "FUJIFILM X-T5"},
		{"", "X100V", "X100V"},
		{"Sony", "", "Sony"},
	}
	for _, c := range cases {
		if got := camera(c.make, c.model); got != c.want {
			t.Errorf("camera(%q, %q) = %q, want %q", c.make, c.model, got, c.want)
		}
	}
}
