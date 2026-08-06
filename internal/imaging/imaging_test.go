package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
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

func TestApplyOrientation(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 3, 2))
	values := []uint8{1, 2, 3, 4, 5, 6}
	for index, value := range values {
		source.SetRGBA(index%3, index/3, color.RGBA{R: value, A: 255})
	}

	tests := []struct {
		orientation int
		want        [][]uint8
	}{
		{1, [][]uint8{{1, 2, 3}, {4, 5, 6}}},
		{2, [][]uint8{{3, 2, 1}, {6, 5, 4}}},
		{3, [][]uint8{{6, 5, 4}, {3, 2, 1}}},
		{4, [][]uint8{{4, 5, 6}, {1, 2, 3}}},
		{5, [][]uint8{{1, 4}, {2, 5}, {3, 6}}},
		{6, [][]uint8{{4, 1}, {5, 2}, {6, 3}}},
		{7, [][]uint8{{6, 3}, {5, 2}, {4, 1}}},
		{8, [][]uint8{{3, 6}, {2, 5}, {1, 4}}},
	}
	for _, test := range tests {
		got := applyOrientation(source, test.orientation)
		if width, height := Size(got); width != len(test.want[0]) || height != len(test.want) {
			t.Fatalf("orientation %d size = %dx%d, want %dx%d", test.orientation, width, height, len(test.want[0]), len(test.want))
		}
		for y, row := range test.want {
			for x, want := range row {
				gotValue := color.RGBAModel.Convert(got.At(x, y)).(color.RGBA).R
				if gotValue != want {
					t.Errorf("orientation %d pixel (%d,%d) = %d, want %d", test.orientation, x, y, gotValue, want)
				}
			}
		}
	}
}

func TestLoadAndDimensionsApplyEXIFOrientation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portrait.jpg")
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, solid(3, 2), nil); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	payload := append([]byte("Exif\x00\x00"), exifOrientationTIFF(6)...)
	var oriented bytes.Buffer
	oriented.Write(data[:2])
	oriented.Write([]byte{0xff, 0xe1})
	if err := binary.Write(&oriented, binary.BigEndian, uint16(len(payload)+2)); err != nil {
		t.Fatal(err)
	}
	oriented.Write(payload)
	oriented.Write(data[2:])
	if err := os.WriteFile(path, oriented.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	width, height, err := Dimensions(path)
	if err != nil {
		t.Fatal(err)
	}
	if width != 2 || height != 3 {
		t.Errorf("dimensions = %dx%d, want 2x3", width, height)
	}
	img, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if width, height := Size(img); width != 2 || height != 3 {
		t.Errorf("loaded size = %dx%d, want 2x3", width, height)
	}
}

func exifOrientationTIFF(orientation uint16) []byte {
	data := make([]byte, 26)
	copy(data, "II")
	binary.LittleEndian.PutUint16(data[2:], 42)
	binary.LittleEndian.PutUint32(data[4:], 8)
	binary.LittleEndian.PutUint16(data[8:], 1)
	binary.LittleEndian.PutUint16(data[10:], 0x0112)
	binary.LittleEndian.PutUint16(data[12:], 3)
	binary.LittleEndian.PutUint32(data[14:], 1)
	binary.LittleEndian.PutUint16(data[18:], orientation)
	return data
}
