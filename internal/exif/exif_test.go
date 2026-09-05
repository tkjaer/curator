package exif

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseXMPTextPrefersDefaultLanguage(t *testing.T) {
	xmp := []byte(`<rdf:Description xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title><rdf:Alt><rdf:li xml:lang="sv">Svensk titel</rdf:li><rdf:li xml:lang="x-default"> Default title </rdf:li></rdf:Alt></dc:title><dc:description><rdf:Alt><rdf:li xml:lang="x-default">First line&#13;&#10;Second line</rdf:li></rdf:Alt></dc:description><dc:subject><rdf:Bag><rdf:li>Travel</rdf:li><rdf:li>Night</rdf:li></rdf:Bag></dc:subject></rdf:Description>`)

	got := parseXMPText(xmp)
	if got.Title != "Default title" || got.Description != "First line\nSecond line" || !reflect.DeepEqual(got.Keywords, []string{"Travel", "Night"}) {
		t.Fatalf("XMP text = %+v", got)
	}
}

func TestExtractTextMetadataPreference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.jpg")
	embeddedXMP := []byte(`<rdf:Description xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Embedded title</dc:title><dc:description>Embedded description</dc:description></rdf:Description>`)
	iptc := append(iptcDataset(2, 5, "IPTC object name"), iptcDataset(2, 120, "IPTC caption")...)
	iptc = append(iptc, iptcDataset(2, 25, "IPTC keyword")...)
	writeMetadataJPEG(t, path, embeddedXMP, iptc)

	meta, err := Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Embedded title" || meta.Description != "Embedded description" || !reflect.DeepEqual(meta.Keywords, []string{"IPTC keyword"}) {
		t.Fatalf("embedded metadata = title %q, description %q", meta.Title, meta.Description)
	}

	if err := os.WriteFile(filepath.Join(dir, "photo.xmp"), []byte(`<rdf:Description xmlns:rdf="urn:rdf" xmlns:dc="urn:dc" dc:title="Sidecar title"/>`), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err = Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Sidecar title" || meta.Description != "Embedded description" {
		t.Fatalf("sidecar preference = title %q, description %q", meta.Title, meta.Description)
	}

	writeMetadataJPEG(t, path, nil, iptc)
	os.Remove(filepath.Join(dir, "photo.xmp"))
	meta, err = Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "IPTC object name" || meta.Description != "IPTC caption" {
		t.Fatalf("IPTC fallback = title %q, description %q", meta.Title, meta.Description)
	}
}

func TestParseIPTCTextEncoding(t *testing.T) {
	tests := []struct {
		name string
		iptc []byte
		want string
	}{
		{
			name: "legacy Windows-1252 without charset",
			iptc: iptcDataset(2, 25, "Hammarby Sj\xf6stad"),
			want: "Hammarby Sjöstad",
		},
		{
			name: "UTF-8 without charset",
			iptc: iptcDataset(2, 25, "Hammarby Sjöstad"),
			want: "Hammarby Sjöstad",
		},
		{
			name: "declared UTF-8",
			iptc: append(iptcDataset(1, 90, "\x1b%G"), iptcDataset(2, 25, "Sjöstad")...),
			want: "Sjöstad",
		},
		{
			name: "normalized Unicode",
			iptc: iptcDataset(2, 25, "Sjo\u0308stad"),
			want: "Sjöstad",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseIPTCDatasets(test.iptc)
			if !reflect.DeepEqual(got.Keywords, []string{test.want}) {
				t.Fatalf("IPTC keywords = %q, want %q", got.Keywords, test.want)
			}
		})
	}
}

func iptcDataset(record, dataset byte, value string) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x1c, record, dataset})
	binary.Write(&buf, binary.BigEndian, uint16(len(value)))
	buf.WriteString(value)
	return buf.Bytes()
}

func writeMetadataJPEG(t *testing.T, path string, xmp, iptc []byte) {
	t.Helper()
	var data bytes.Buffer
	data.Write([]byte{0xff, 0xd8})
	writeSegment := func(marker byte, payload []byte) {
		data.Write([]byte{0xff, marker})
		binary.Write(&data, binary.BigEndian, uint16(len(payload)+2))
		data.Write(payload)
	}
	if xmp != nil {
		writeSegment(0xe1, append(append([]byte{}, xmpHeader...), xmp...))
	}
	if iptc != nil {
		writeSegment(0xed, iptc)
	}
	data.Write([]byte{0xff, 0xd9})
	if err := os.WriteFile(path, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestXMPLensFallback(t *testing.T) {
	xmp := []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/" xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:crs="http://ns.adobe.com/camera-raw-settings/1.0/"><rdf:RDF><rdf:Description crs:LensProfileName="Adobe (Voigtlander VM 15mm f/4.5)"/></rdf:RDF></x:xmpmeta>`)
	payload := append(append([]byte{}, xmpHeader...), xmp...)

	path := filepath.Join(t.TempDir(), "xmp.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0xff, 0xd8, 0xff, 0xe1})
	if err := binary.Write(f, binary.BigEndian, uint16(len(payload)+2)); err != nil {
		t.Fatal(err)
	}
	f.Write(payload)
	f.Write([]byte{0xff, 0xd9})
	f.Close()

	lens, profile := xmpLens(path)
	if got := xmpLensValue("", lens, profile); got != "Voigtlander VM 15mm f/4.5" {
		t.Fatalf("XMP lens = %q", got)
	}
	meta, err := Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Lens != "" || meta.XMPLens != "Voigtlander VM 15mm f/4.5" {
		t.Fatalf("extracted lenses = EXIF %q, XMP %q", meta.Lens, meta.XMPLens)
	}
	direct := []byte(`<rdf:Description xmlns:rdf="urn:rdf" xmlns:aux="urn:aux" xmlns:crs="urn:crs" aux:Lens="Direct lens" crs:LensProfileName="Adobe (Profile lens)"/>`)
	if got := parseXMPLens(direct); got != "Direct lens" {
		t.Fatalf("direct XMP lens = %q", got)
	}
}

func TestXMPLensValueRejectsCameraEquivalentProfile(t *testing.T) {
	for _, profile := range []string{
		"Fujifilm X100S",
		" FUJIFILM   X100S ",
		"FUJIFILM-X100S",
		"Fujifilm X100-S",
	} {
		if got := xmpLensValue("FUJIFILM X100S", "", profile); got != "" {
			t.Errorf("camera-equivalent profile %q = %q, want empty", profile, got)
		}
	}
	if got := xmpLensValue("FUJIFILM X100S", "", "Fujinon 23mm f/2"); got != "Fujinon 23mm f/2" {
		t.Errorf("real profile = %q", got)
	}
	if got := xmpLensValue("FUJIFILM X100S", "Fujifilm X100S", "Camera profile"); got != "Fujifilm X100S" {
		t.Errorf("explicit XMP lens = %q", got)
	}
}

func TestExtractRejectsCameraEquivalentProfileButPreservesDirectLens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x100s.jpg")
	profile := []byte(`<rdf:Description xmlns:rdf="urn:rdf" xmlns:crs="urn:crs" crs:LensProfileName="Adobe (Fujifilm-X100 S)"/>`)
	writeEXIFXMPJPEG(t, path, "FUJIFILM", "X100S", profile)

	meta, err := Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Camera != "FUJIFILM X100S" || meta.XMPLens != "" {
		t.Fatalf("camera profile extraction = %+v", meta)
	}

	direct := []byte(`<rdf:Description xmlns:rdf="urn:rdf" xmlns:aux="urn:aux" aux:Lens="Fujifilm-X100 S"/>`)
	writeEXIFXMPJPEG(t, path, "FUJIFILM", "X100S", direct)
	meta, err = Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.XMPLens != "Fujifilm-X100 S" {
		t.Fatalf("direct lens extraction = %+v", meta)
	}
}

func writeEXIFXMPJPEG(t *testing.T, path, makeName, modelName string, xmp []byte) {
	t.Helper()
	makeValue := append([]byte(makeName), 0)
	modelValue := append([]byte(modelName), 0)
	const dataOffset = 8 + 2 + 2*12 + 4

	var tiff bytes.Buffer
	tiff.WriteString("II")
	binary.Write(&tiff, binary.LittleEndian, uint16(42))
	binary.Write(&tiff, binary.LittleEndian, uint32(8))
	binary.Write(&tiff, binary.LittleEndian, uint16(2))
	for _, entry := range []struct {
		tag    uint16
		value  []byte
		offset uint32
	}{
		{tag: 0x010f, value: makeValue, offset: dataOffset},
		{tag: 0x0110, value: modelValue, offset: dataOffset + uint32(len(makeValue))},
	} {
		binary.Write(&tiff, binary.LittleEndian, entry.tag)
		binary.Write(&tiff, binary.LittleEndian, uint16(2))
		binary.Write(&tiff, binary.LittleEndian, uint32(len(entry.value)))
		binary.Write(&tiff, binary.LittleEndian, entry.offset)
	}
	binary.Write(&tiff, binary.LittleEndian, uint32(0))
	tiff.Write(makeValue)
	tiff.Write(modelValue)

	var data bytes.Buffer
	data.Write([]byte{0xff, 0xd8})
	for _, payload := range [][]byte{
		append([]byte("Exif\x00\x00"), tiff.Bytes()...),
		append(append([]byte{}, xmpHeader...), xmp...),
	} {
		data.Write([]byte{0xff, 0xe1})
		binary.Write(&data, binary.BigEndian, uint16(len(payload)+2))
		data.Write(payload)
	}
	data.Write([]byte{0xff, 0xd9})
	if err := os.WriteFile(path, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

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

func TestExtractSidecarLens(t *testing.T) {
	tests := []struct {
		name        string
		sidecarName string
		xmp         string
		wantLens    string
	}{
		{
			name:        "aux lens attribute",
			sidecarName: "photo.xmp",
			xmp:         `<rdf:Description xmlns:rdf="urn:rdf" xmlns:aux="http://ns.adobe.com/exif/1.0/aux/" aux:Lens="Voigtlander 15mm f/4.5"/>`,
			wantLens:    "Voigtlander 15mm f/4.5",
		},
		{
			name:        "exifEX lens element and filename sidecar",
			sidecarName: "photo.jpg.xmp",
			xmp:         `<rdf:Description xmlns:rdf="urn:rdf" xmlns:exifEX="http://cipa.jp/exif/1.0/"><exifEX:LensModel>7Artisans 35mm f/1.2</exifEX:LensModel></rdf:Description>`,
			wantLens:    "7Artisans 35mm f/1.2",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "photo.jpg")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 10, 10)), nil); err != nil {
				t.Fatal(err)
			}
			f.Close()
			if err := os.WriteFile(filepath.Join(dir, test.sidecarName), []byte(test.xmp), 0o644); err != nil {
				t.Fatal(err)
			}

			meta, err := Extract(path)
			if err != nil {
				t.Fatal(err)
			}
			if meta.SidecarLens != test.wantLens {
				t.Fatalf("sidecar lens = %q, want %q", meta.SidecarLens, test.wantLens)
			}
		})
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
