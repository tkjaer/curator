package model

import "testing"

func TestClassifyAspect(t *testing.T) {
	cases := []struct {
		w, h int
		want Aspect
	}{
		{3000, 2000, AspectLandscape}, // 3:2
		{4000, 3000, AspectLandscape}, // 4:3
		{1920, 1080, AspectLandscape}, // 16:9 stays landscape
		{2000, 3000, AspectPortrait},  // 2:3
		{2000, 2000, AspectSquare},
		{6500, 2400, AspectPano},     // 65:24
		{2400, 6500, AspectPortrait}, // extreme vertical
		{807, 2000, AspectPortrait},  // tall portrait stays in justified rows
		{0, 0, AspectLandscape},      // unknown
	}
	for _, c := range cases {
		if got := ClassifyAspect(c.w, c.h); got != c.want {
			t.Errorf("ClassifyAspect(%d,%d) = %q, want %q", c.w, c.h, got, c.want)
		}
	}
}
