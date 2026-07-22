package render

import (
	"math"
	"testing"
)

func photo(w, h int, aspect string) PhotoView {
	return PhotoView{Width: w, Height: h, Aspect: aspect}
}

func TestJustifyFillsContainerWidth(t *testing.T) {
	photos := []PhotoView{
		photo(3000, 2000, "landscape"),
		photo(2000, 2000, "square"),
		photo(3000, 2000, "landscape"),
		photo(2000, 3000, "portrait"),
		photo(3000, 2000, "landscape"),
	}
	const container, target, gap = 1000, 300, 8

	rows := Justify(photos, container, target, gap, false)
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}

	// Every row except the last should fill the container width.
	for i, row := range rows[:len(rows)-1] {
		total := gap * (len(row.Photos) - 1)
		for _, p := range row.Photos {
			total += int(math.Round(float64(p.Width) / float64(p.Height) * float64(row.Height)))
		}
		if math.Abs(float64(total-container)) > 2 {
			t.Errorf("row %d width = %d, want ~%d", i, total, container)
		}
	}
}

func TestJustifyPanoFullWidth(t *testing.T) {
	photos := []PhotoView{
		photo(3000, 2000, "landscape"),
		photo(6500, 2400, "pano"),
		photo(3000, 2000, "landscape"),
	}

	rows := Justify(photos, 1000, 300, 8, true)

	var panoRow int = -1
	for i, row := range rows {
		for _, p := range row.Photos {
			if p.Aspect == "pano" {
				if len(row.Photos) != 1 {
					t.Fatalf("pano row has %d photos, want 1", len(row.Photos))
				}
				panoRow = i
			}
		}
	}
	if panoRow == -1 {
		t.Fatal("pano was not placed in its own row")
	}
}

func TestJustifyEmpty(t *testing.T) {
	if rows := Justify(nil, 1000, 300, 8, false); rows != nil {
		t.Errorf("expected nil rows, got %v", rows)
	}
	if rows := Justify([]PhotoView{photo(3, 2, "landscape")}, 0, 300, 8, false); rows != nil {
		t.Errorf("expected nil rows for zero width, got %v", rows)
	}
}

func TestJustifyLastRowNotTallerThanPrevious(t *testing.T) {
	// Four 3:2 photos: the first rows fill the width (scaling below the target
	// height), leaving a single photo on the last row. That lone photo must not
	// tower over the row above it.
	photos := []PhotoView{
		photo(3000, 2000, "landscape"),
		photo(3000, 2000, "landscape"),
		photo(3000, 2000, "landscape"),
		photo(3000, 2000, "landscape"),
	}
	rows := Justify(photos, 1000, 300, 8, false)
	if len(rows) < 2 {
		t.Fatalf("expected multiple rows, got %d", len(rows))
	}
	last := rows[len(rows)-1]
	prev := rows[len(rows)-2]
	if len(last.Photos) != 1 {
		t.Fatalf("expected a single photo on the last row, got %d", len(last.Photos))
	}
	if last.Height > prev.Height {
		t.Errorf("last row height %d taller than previous row %d", last.Height, prev.Height)
	}
}

func TestJustifyPanoInlineByDefault(t *testing.T) {
	// With panoFullWidth off, panoramas flow inline and pack together rather than
	// each taking its own row.
	photos := []PhotoView{
		photo(6500, 2400, "pano"),
		photo(6500, 2400, "pano"),
		photo(6500, 2400, "pano"),
	}
	rows := Justify(photos, 1000, 300, 8, false)
	if len(rows) == 0 || len(rows[0].Photos) < 2 {
		t.Fatalf("expected panoramas to pack inline, got rows %+v", rows)
	}
}

func TestJustifyPanoFullWidthCapped(t *testing.T) {
	// With panoFullWidth on, a wide panorama gets its own row but is capped at
	// the target height (not stretched to fill the width) and centered.
	rows := Justify([]PhotoView{photo(6500, 2400, "pano")}, 1000, 300, 8, true)
	if len(rows) != 1 || len(rows[0].Photos) != 1 {
		t.Fatalf("expected one pano row, got %+v", rows)
	}
	r := rows[0]
	if r.Height != 300 {
		t.Errorf("pano row height = %d, want it capped at target 300", r.Height)
	}
	if !r.Center {
		t.Error("capped pano row should be centered")
	}
	if r.Photos[0].FlexBasis >= 100 {
		t.Errorf("capped pano should be narrower than full width, got flex-basis %.2f", r.Photos[0].FlexBasis)
	}
}
