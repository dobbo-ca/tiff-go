package tiff_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	tiff "github.com/dobbo-ca/tiff-go"
	"github.com/dobbo-ca/tiff-go/internal/fixture"
)

// testdata/synthetic/cmyk_jpeg_*.tif are 4-component JPEG pages written by libtiff
// (Pillow, tiffcp); the .raw next to each is what Pillow (libtiff) reads from it.
// libtiff does no colour conversion for them, so the samples must be equal.
func TestJPEGCMYKMatchesLibtiff(t *testing.T) {
	for _, name := range []string{"strip1", "bs", "bt"} {
		b, _ := os.ReadFile(filepath.Join("testdata", "synthetic", "cmyk_jpeg_"+name+".tif"))
		want, _ := os.ReadFile(filepath.Join("testdata", "synthetic", "cmyk_jpeg_"+name+".raw"))
		for _, lim := range []tiff.Limits{{}, {MaxBandBytes: 9000}} {
			got, _, err := decode(t, b, lim, 0)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s band=%d: differs from libtiff", name, lim.MaxBandBytes)
			}
		}
	}
}

// A restart interval changes nothing: jpegtran -restart of the plain stream is the same samples.
func TestJPEGCMYKRestart(t *testing.T) {
	var got [2][]byte
	for i, n := range []string{"plain", "restart"} {
		j, _ := os.ReadFile(filepath.Join("testdata", "synthetic", "cmyk_jpeg_"+n+".jpg"))
		b := fixture.File{Pages: []fixture.Page{{W: 70, H: 16, SPP: 4, BPS: 8, Photometric: 5, Compression: 7, RowsPerStrip: 16, Chunks: [][]byte{j}}}}.Bytes()
		var err error
		if got[i], _, err = decode(t, b, tiff.Limits{}, 0); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	if len(got[0]) != 70*16*4 || !bytes.Equal(got[0], got[1]) {
		t.Fatal("restart stream decodes differently")
	}
}

// Truncated entropy data and a marker where data should be are corrupt, not a panic.
func TestJPEGCMYKCorrupt(t *testing.T) {
	j, _ := os.ReadFile(filepath.Join("testdata", "synthetic", "cmyk_jpeg_plain.jpg"))
	for _, c := range [][]byte{j[:len(j)/2], j[:len(j)-200], append(j[:len(j)/2:len(j)/2], 0xff, 0xd9)} {
		b := fixture.File{Pages: []fixture.Page{{W: 70, H: 16, SPP: 4, BPS: 8, Photometric: 5, Compression: 7, RowsPerStrip: 16, Chunks: [][]byte{c}}}}.Bytes()
		if _, _, err := decode(t, b, tiff.Limits{}, 0); err == nil {
			t.Error("accepted")
		}
	}
}
