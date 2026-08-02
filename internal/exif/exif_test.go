package exif

import (
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

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

	if got := xmpLens(path); got != "Voigtlander VM 15mm f/4.5" {
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
