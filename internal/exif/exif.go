// Package exif extracts a small, presentation-focused set of camera metadata
// from JPEG files, plus the raw tag set as JSON. Missing or absent EXIF is not
// an error: callers get a zero Data.
package exif

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// Data is the normalized metadata Curator stores and displays.
type Data struct {
	TakenAt     *time.Time
	Camera      string
	Lens        string
	SidecarLens string
	XMPLens     string
	Aperture    string
	Shutter     string
	ISO         string
	Focal       string
	Raw         string // JSON of all EXIF tags
}

// Extract reads metadata from the image at path. A file without EXIF yields an
// empty Data and no error.
func Extract(path string) (Data, error) {
	sidecarLens, sidecarProfile := sidecarXMP(path)
	f, err := os.Open(path)
	if err != nil {
		return Data{}, err
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		if sidecarProfile == "" {
			sidecarProfile = xmpLens(path)
		}
		return Data{SidecarLens: sidecarLens, XMPLens: sidecarProfile}, nil
	}

	d := Data{
		Camera:      camera(str(x, exif.Make), str(x, exif.Model)),
		Lens:        str(x, exif.LensModel),
		SidecarLens: sidecarLens,
		Aperture:    aperture(x),
		Shutter:     shutter(x),
		ISO:         intStr(x, exif.ISOSpeedRatings),
		Focal:       focal(x),
	}
	if d.Lens == "" {
		d.XMPLens = sidecarProfile
		if d.XMPLens == "" {
			d.XMPLens = xmpLens(path)
		}
	}
	if t, err := x.DateTime(); err == nil {
		d.TakenAt = &t
	}
	if raw, err := x.MarshalJSON(); err == nil {
		d.Raw = string(raw)
	}
	return d, nil
}

var xmpHeader = []byte("http://ns.adobe.com/xap/1.0/\x00")

func xmpLens(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var soi [2]byte
	if _, err := io.ReadFull(f, soi[:]); err != nil || soi != [2]byte{0xff, 0xd8} {
		return ""
	}
	for {
		marker, err := nextJPEGMarker(f)
		if err != nil || marker == 0xda || marker == 0xd9 {
			return ""
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd8 {
			continue
		}
		var size uint16
		if err := binary.Read(f, binary.BigEndian, &size); err != nil || size < 2 {
			return ""
		}
		payload := make([]byte, int(size)-2)
		if _, err := io.ReadFull(f, payload); err != nil {
			return ""
		}
		if marker == 0xe1 && bytes.HasPrefix(payload, xmpHeader) {
			return parseXMPLens(payload[len(xmpHeader):])
		}
	}
}

func nextJPEGMarker(r io.Reader) (byte, error) {
	var b [1]byte
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		if b[0] != 0xff {
			continue
		}
		for {
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return 0, err
			}
			if b[0] != 0xff {
				return b[0], nil
			}
		}
	}
}

func parseXMPLens(data []byte) string {
	lens, profile := parseXMP(data)
	if lens != "" {
		return lens
	}
	return profile
}

func parseXMP(data []byte) (lens, profile string) {
	var field string
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch value := token.(type) {
		case xml.StartElement:
			field = value.Name.Local
			for _, attr := range value.Attr {
				switch attr.Name.Local {
				case "Lens", "LensModel":
					if lens == "" {
						lens = strings.TrimSpace(attr.Value)
					}
				case "LensProfileName":
					if profile == "" {
						profile = strings.TrimSpace(attr.Value)
					}
				}
			}
		case xml.CharData:
			text := strings.TrimSpace(string(value))
			if text == "" {
				continue
			}
			switch field {
			case "Lens", "LensModel":
				if lens == "" {
					lens = text
				}
			case "LensProfileName":
				if profile == "" {
					profile = text
				}
			}
		case xml.EndElement:
			field = ""
		}
	}
	if strings.HasPrefix(profile, "Adobe (") && strings.HasSuffix(profile, ")") {
		profile = strings.TrimSuffix(strings.TrimPrefix(profile, "Adobe ("), ")")
	}
	return lens, profile
}

// SidecarPath returns the adjacent XMP sidecar for an image, or an empty
// string when none exists. Both common basename and filename forms are read.
func SidecarPath(imagePath string) string {
	ext := filepath.Ext(imagePath)
	base := strings.TrimSuffix(imagePath, ext)
	for _, candidate := range []string{base + ".xmp", base + ".XMP", imagePath + ".xmp", imagePath + ".XMP"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func sidecarXMP(imagePath string) (lens, profile string) {
	path := SidecarPath(imagePath)
	if path == "" {
		return "", ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	return parseXMP(data)
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
