package tiff_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tiff "github.com/dobbo-ca/tiff-go"
	"github.com/dobbo-ca/tiff-go/internal/fixture"
)

// testdata/golden/<src>_<codec>_<strip|tile>.tif were written by libtiff's
// tiffcp from <src>.raw's image (37x29); lossless files must decode to
// <src>.raw exactly, JPEG ones to libtiff's own decode (<name>.ref) within
// IDCT rounding.
func TestLibtiffOracle(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "golden", "*_*_*.tif"))
	if len(files) < 30 {
		t.Fatalf("only %d oracle files", len(files))
	}
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".tif")
		if strings.HasPrefix(name, "bw") {
			continue
		}
		want, err := os.ReadFile(strings.TrimSuffix(path, ".tif") + ".ref")
		jpeg := err == nil
		if !jpeg {
			want, _ = os.ReadFile(filepath.Join("testdata", "golden", strings.SplitN(name, "_", 2)[0]+".raw"))
		}
		b, _ := os.ReadFile(path)
		for _, lim := range []tiff.Limits{{}, {MaxBandBytes: 2048}} {
			got, _, err := decode(t, b, lim, 0)
			if err != nil {
				t.Errorf("%s band=%d: %v", name, lim.MaxBandBytes, err)
				continue
			}
			if len(got) != len(want) {
				t.Errorf("%s: %d bytes, want %d", name, len(got), len(want))
				continue
			}
			maxd := 0
			for i := range got {
				maxd = max(maxd, abs(int(got[i])-int(want[i])))
			}
			if (!jpeg && !bytes.Equal(got, want)) || maxd > 8 {
				t.Errorf("%s band=%d: differs from libtiff (max diff %d)", name, lim.MaxBandBytes, maxd)
			}
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// jpeg-go decodes only baseline interleaved (or single-component) frames a band
// at a time, so any other frame is decoded whole per band. It must still decode,
// and to what one unbanded decode gives.
func TestJPEGAnyFrameBands(t *testing.T) {
	for _, name := range []string{"jpeg_baseline", "jpeg_noninterleaved", "jpeg_progressive_fill"} {
		j, _ := os.ReadFile(filepath.Join("testdata", "synthetic", name+".jpg"))
		b := fixture.File{Pages: []fixture.Page{{W: 16, H: 64, SPP: 3, BPS: 8, Photometric: 2, Compression: 7, RowsPerStrip: 64, Chunks: [][]byte{j}}}}.Bytes()
		want, _, err := decode(t, b, tiff.Limits{}, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _, err := decode(t, b, tiff.Limits{MaxBandBytes: 4 * 16 * 8}, 0)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: banded %v, equal %v", name, err, bytes.Equal(got, want))
		}
	}
}
