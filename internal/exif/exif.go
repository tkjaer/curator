// Package exif extracts a small, presentation-focused set of camera metadata
// from JPEG files, plus the raw tag set as JSON. Missing or absent EXIF is not
// an error: callers get a zero Data.
package exif

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// Data is the normalized metadata Curator stores and displays.
type Data struct {
	TakenAt  *time.Time
	Camera   string
	Lens     string
	Aperture string
	Shutter  string
	ISO      string
	Focal    string
	Raw      string // JSON of all EXIF tags
}

// Extract reads metadata from the image at path. A file without EXIF yields an
// empty Data and no error.
func Extract(path string) (Data, error) {
	f, err := os.Open(path)
	if err != nil {
		return Data{}, err
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		return Data{}, nil
	}

	d := Data{
		Camera:   camera(str(x, exif.Make), str(x, exif.Model)),
		Lens:     str(x, exif.LensModel),
		Aperture: aperture(x),
		Shutter:  shutter(x),
		ISO:      intStr(x, exif.ISOSpeedRatings),
		Focal:    focal(x),
	}
	if t, err := x.DateTime(); err == nil {
		d.TakenAt = &t
	}
	if raw, err := x.MarshalJSON(); err == nil {
		d.Raw = string(raw)
	}
	return d, nil
}

func str(x *exif.Exif, field exif.FieldName) string {
	tag, err := x.Get(field)
	if err != nil {
		return ""
	}
	s, err := tag.StringVal()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.Trim(s, "\x00"))
}

// intStr reads an integer EXIF tag (such as ISO) and formats it as a string.
func intStr(x *exif.Exif, field exif.FieldName) string {
	tag, err := x.Get(field)
	if err != nil {
		return ""
	}
	v, err := tag.Int(0)
	if err != nil {
		return ""
	}
	return strconv.Itoa(v)
}

func camera(make, model string) string {
	make = strings.TrimSpace(make)
	model = strings.TrimSpace(model)
	switch {
	case model == "":
		return make
	case make == "":
		return model
	}
	first := strings.ToLower(strings.Fields(make)[0])
	if strings.HasPrefix(strings.ToLower(model), first) {
		return model
	}
	return make + " " + model
}

func aperture(x *exif.Exif) string {
	v, ok := ratio(x, exif.FNumber)
	if !ok {
		return ""
	}
	return "f/" + trimFloat(v)
}

func focal(x *exif.Exif) string {
	v, ok := ratio(x, exif.FocalLength)
	if !ok {
		return ""
	}
	return trimFloat(v) + " mm"
}

func shutter(x *exif.Exif) string {
	tag, err := x.Get(exif.ExposureTime)
	if err != nil {
		return ""
	}
	num, den, err := tag.Rat2(0)
	if err != nil || num == 0 || den == 0 {
		return ""
	}
	v := float64(num) / float64(den)
	if v < 1 {
		return fmt.Sprintf("1/%d", int(math.Round(1/v)))
	}
	return trimFloat(v) + "s"
}

func ratio(x *exif.Exif, field exif.FieldName) (float64, bool) {
	tag, err := x.Get(field)
	if err != nil {
		return 0, false
	}
	num, den, err := tag.Rat2(0)
	if err != nil || den == 0 {
		return 0, false
	}
	return float64(num) / float64(den), true
}

func trimFloat(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}
