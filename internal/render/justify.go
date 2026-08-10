package render

// aspectRatio returns width/height for a photo, falling back to its aspect
// class when pixel dimensions are missing.
func aspectRatio(p PhotoView) float64 {
	if p.Width > 0 && p.Height > 0 {
		return float64(p.Width) / float64(p.Height)
	}
	switch p.Aspect {
	case "square":
		return 1
	case "pano":
		return 65.0 / 24.0
	case "portrait":
		return 2.0 / 3.0
	default:
		return 3.0 / 2.0
	}
}

// Justify arranges photos into rows that fill containerWidth, scaling each row
// to sit near targetHeight. When panoFullWidth is set, panoramas prefer their
// own rows but may share spare width with adjacent narrow photos that fit at the
// target height. The last row keeps its natural height rather than stretching.
func Justify(photos []PhotoView, containerWidth, targetHeight, gap int, panoFullWidth bool) []GridRow {
	if containerWidth <= 0 || targetHeight <= 0 {
		return nil
	}

	var rows []GridRow
	var row []PhotoView
	var sumRatio float64

	flush := func(fill, center bool) {
		if len(row) == 0 {
			return
		}
		laidOut := layoutRow(row, sumRatio, containerWidth, targetHeight, gap, fill)
		laidOut.Center = center
		rows = append(rows, laidOut)
		row = nil
		sumRatio = 0
	}

	for index := 0; index < len(photos); index++ {
		p := photos[index]
		if panoFullWidth && p.Aspect == "pano" && aspectRatio(p) > 1 {
			panoRatio := aspectRatio(p)
			if len(row) > 0 && !fitsAtHeight(sumRatio+panoRatio, len(row)+1, containerWidth, targetHeight, gap) {
				flush(false, true)
			}
			row = append(row, p)
			sumRatio += panoRatio

			for index+1 < len(photos) {
				next := photos[index+1]
				if next.Aspect == "pano" && aspectRatio(next) > 1 {
					break
				}
				nextRatio := aspectRatio(next)
				if !fitsAtHeight(sumRatio+nextRatio, len(row)+1, containerWidth, targetHeight, gap) {
					break
				}
				index++
				row = append(row, next)
				sumRatio += nextRatio
			}

			if len(row) == 1 {
				rows = append(rows, panoRow(p, containerWidth, targetHeight))
				row = nil
				sumRatio = 0
			} else {
				flush(false, true)
			}
			continue
		}

		row = append(row, p)
		sumRatio += aspectRatio(p)

		gaps := float64(gap * (len(row) - 1))
		if sumRatio*float64(targetHeight)+gaps >= float64(containerWidth) {
			flush(true, false)
		}
	}

	// The last, partial row keeps its natural height, but is never taller than
	// the row above it so a lone wide image doesn't loom larger than its
	// neighbours.
	if len(row) > 0 {
		h := targetHeight
		if n := len(rows); n > 0 && rows[n-1].Height < h {
			h = rows[n-1].Height
		}
		rows = append(rows, layoutRow(row, sumRatio, containerWidth, h, gap, false))
	}
	return rows
}

func fitsAtHeight(sumRatio float64, photoCount, containerWidth, height, gap int) bool {
	width := sumRatio*float64(height) + float64(gap*(photoCount-1))
	return width <= float64(containerWidth)
}

// panoRow lays out a panorama on its own row. It fills the container width but
// caps the height at targetHeight so a very wide image does not tower over the
// surrounding rows; when capped it is narrower than the container and centered.
func panoRow(p PhotoView, containerWidth, targetHeight int) GridRow {
	r := aspectRatio(p)
	height := int(float64(containerWidth) / r)
	if height > targetHeight {
		height = targetHeight
	}
	p.RowHeight = height
	p.FlexBasis = r * float64(height) / float64(containerWidth) * 100
	return GridRow{Photos: []PhotoView{p}, Height: height, Center: true}
}

// layoutRow sizes one row. When fill is true the row is scaled to occupy the
// full container width; otherwise it stays at targetHeight (used for the last,
// partial row).
func layoutRow(photos []PhotoView, sumRatio float64, containerWidth, targetHeight, gap int, fill bool) GridRow {
	avail := float64(containerWidth - gap*(len(photos)-1))

	height := targetHeight
	if fill && sumRatio > 0 {
		height = int(avail / sumRatio)
	}

	out := make([]PhotoView, len(photos))
	for i, p := range photos {
		r := aspectRatio(p)
		width := r * float64(height)
		p.RowHeight = height
		p.FlexBasis = width / float64(containerWidth) * 100
		out[i] = p
	}
	return GridRow{Photos: out, Height: height}
}
