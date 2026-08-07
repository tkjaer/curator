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
	"unicode/utf16"
	"unicode/utf8"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
)

// Data is the normalized metadata Curator stores and displays.
type Data struct {
	Title       string
	Description string
	Keywords    []string
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

// Text metadata preferences are ordered from most to least preferred.
var (
	TitlePreference       = []string{"sidecar XMP dc:title", "embedded XMP dc:title", "IPTC ObjectName", "IPTC Headline", "EXIF XPTitle"}
	DescriptionPreference = []string{"sidecar XMP dc:description", "embedded XMP dc:description", "IPTC Caption-Abstract", "EXIF ImageDescription", "EXIF XPComment"}
)

// Extract reads metadata from the image at path. A file without EXIF yields an
// empty Data and no error.
func Extract(path string) (Data, error) {
	sidecarLens, sidecarProfile := sidecarXMP(path)
	sidecarText := sidecarXMPText(path)
	embeddedText, iptcText := embeddedTextMetadata(path)
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
		return Data{
			Title:       firstText(sidecarText.Title, embeddedText.Title, iptcText.ObjectName, iptcText.Headline),
			Description: firstText(sidecarText.Description, embeddedText.Description, iptcText.Caption),
			Keywords:    mergeKeywords(sidecarText.Keywords, embeddedText.Keywords, iptcText.Keywords),
			SidecarLens: sidecarLens,
			XMPLens:     sidecarProfile,
		}, nil
	}

	d := Data{
		Title:       firstText(sidecarText.Title, embeddedText.Title, iptcText.ObjectName, iptcText.Headline, exifText(x, exif.XPTitle)),
		Description: firstText(sidecarText.Description, embeddedText.Description, iptcText.Caption, str(x, exif.ImageDescription), exifText(x, exif.XPComment)),
		Keywords:    mergeKeywords(sidecarText.Keywords, embeddedText.Keywords, iptcText.Keywords),
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

type textMetadata struct {
	Title       string
	Description string
	Keywords    []string
}

type iptcMetadata struct {
	ObjectName string
	Headline   string
	Caption    string
	Keywords   []string
}

func parseXMPText(data []byte) textMetadata {
	type candidate struct {
		value string
		lang  string
	}
	var title, description []candidate
	var keywords []string
	var field string
	var lang string
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "title", "description", "subject":
				field = value.Name.Local
			}
			for _, attr := range value.Attr {
				switch attr.Name.Local {
				case "lang":
					lang = attr.Value
				case "title":
					title = append(title, candidate{value: attr.Value})
				case "description":
					description = append(description, candidate{value: attr.Value})
				case "subject":
					keywords = append(keywords, attr.Value)
				}
			}
		case xml.CharData:
			text := normalizeText(string(value))
			if text == "" || field == "" {
				continue
			}
			if field == "title" {
				title = append(title, candidate{value: text, lang: lang})
			} else if field == "description" {
				description = append(description, candidate{value: text, lang: lang})
			} else {
				keywords = append(keywords, text)
			}
		case xml.EndElement:
			if value.Name.Local == "li" {
				lang = ""
			}
			if value.Name.Local == field {
				field = ""
			}
		}
	}
	pick := func(values []candidate) string {
		for _, value := range values {
			if value.lang == "x-default" && normalizeText(value.value) != "" {
				return normalizeText(value.value)
			}
		}
		for _, value := range values {
			if text := normalizeText(value.value); text != "" {
				return text
			}
		}
		return ""
	}
	return textMetadata{Title: pick(title), Description: pick(description), Keywords: keywords}
}

func embeddedTextMetadata(path string) (textMetadata, iptcMetadata) {
	f, err := os.Open(path)
	if err != nil {
		return textMetadata{}, iptcMetadata{}
	}
	defer f.Close()
	var soi [2]byte
	if _, err := io.ReadFull(f, soi[:]); err != nil || soi != [2]byte{0xff, 0xd8} {
		return textMetadata{}, iptcMetadata{}
	}
	var xmp textMetadata
	var iptc iptcMetadata
	for {
		marker, err := nextJPEGMarker(f)
		if err != nil || marker == 0xda || marker == 0xd9 {
			return xmp, iptc
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd8 {
			continue
		}
		var size uint16
		if err := binary.Read(f, binary.BigEndian, &size); err != nil || size < 2 {
			return xmp, iptc
		}
		payload := make([]byte, int(size)-2)
		if _, err := io.ReadFull(f, payload); err != nil {
			return xmp, iptc
		}
		if marker == 0xe1 && bytes.HasPrefix(payload, xmpHeader) {
			xmp = parseXMPText(payload[len(xmpHeader):])
		} else if marker == 0xed {
			iptc = parseIPTC(payload)
		}
	}
}

func parseIPTC(data []byte) iptcMetadata {
	const photoshopHeader = "Photoshop 3.0\x00"
	if bytes.HasPrefix(data, []byte(photoshopHeader)) {
		data = data[len(photoshopHeader):]
		for len(data) >= 12 {
			if string(data[:4]) != "8BIM" {
				break
			}
			resourceID := binary.BigEndian.Uint16(data[4:6])
			nameSize := int(data[6])
			nameEnd := 7 + nameSize
			if nameEnd > len(data) {
				break
			}
			if nameEnd%2 != 0 {
				nameEnd++
			}
			if nameEnd+4 > len(data) {
				break
			}
			size := int(binary.BigEndian.Uint32(data[nameEnd : nameEnd+4]))
			start := nameEnd + 4
			if size < 0 || start+size > len(data) {
				break
			}
			if resourceID == 0x0404 {
				return parseIPTCDatasets(data[start : start+size])
			}
			end := start + size
			if end%2 != 0 {
				end++
			}
			data = data[end:]
		}
	}
	return parseIPTCDatasets(data)
}

func parseIPTCDatasets(data []byte) iptcMetadata {
	var metadata iptcMetadata
	utf8Declared := false
	for len(data) >= 5 {
		if data[0] != 0x1c {
			data = data[1:]
			continue
		}
		record, dataset := data[1], data[2]
		size := int(binary.BigEndian.Uint16(data[3:5]))
		if size&0x8000 != 0 || 5+size > len(data) {
			break
		}
		raw := data[5 : 5+size]
		if record == 1 && dataset == 90 {
			utf8Declared = bytes.Equal(raw, []byte{0x1b, 0x25, 0x47})
			data = data[5+size:]
			continue
		}
		value := normalizeText(decodeIPTCText(raw, utf8Declared))
		if record == 2 {
			switch dataset {
			case 25:
				if value != "" {
					metadata.Keywords = append(metadata.Keywords, value)
				}
			case 5:
				metadata.ObjectName = firstText(metadata.ObjectName, value)
			case 105:
				metadata.Headline = firstText(metadata.Headline, value)
			case 120:
				metadata.Caption = firstText(metadata.Caption, value)
			}
		}
		data = data[5+size:]
	}
	return metadata
}

func decodeIPTCText(value []byte, utf8Declared bool) string {
	if utf8Declared || utf8.Valid(value) {
		return strings.ToValidUTF8(string(value), "\uFFFD")
	}
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(value)
	if err != nil {
		return strings.ToValidUTF8(string(value), "\uFFFD")
	}
	return string(decoded)
}

func mergeKeywords(groups ...[]string) []string {
	var keywords []string
	for _, group := range groups {
		keywords = append(keywords, group...)
	}
	return keywords
}

func firstText(values ...string) string {
	for _, value := range values {
		if text := normalizeText(value); text != "" {
			return text
		}
	}
	return ""
}

func normalizeText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return norm.NFC.String(strings.TrimSpace(strings.Trim(value, "\x00")))
}

func exifText(x *exif.Exif, field exif.FieldName) string {
	tag, err := x.Get(field)
	if err != nil {
		return ""
	}
	if value, err := tag.StringVal(); err == nil {
		if text := normalizeText(value); text != "" {
			return text
		}
	}
	raw := strings.Trim(tag.String(), "\"[] ")
	parts := strings.Fields(raw)
	units := make([]uint16, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.ParseUint(strings.TrimSuffix(part, ","), 10, 16)
		if err != nil || value == 0 {
			continue
		}
		units = append(units, uint16(value))
	}
	return normalizeText(string(utf16.Decode(units)))
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

func sidecarXMPText(imagePath string) textMetadata {
	path := SidecarPath(imagePath)
	if path == "" {
		return textMetadata{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return textMetadata{}
	}
	return parseXMPText(data)
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
