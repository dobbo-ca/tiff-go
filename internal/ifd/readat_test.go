package ifd

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dobbo-ca/tiff-go/internal/e"
)

func TestReadAtNearMaxOffset(t *testing.T) {
	f := &File{ra: bytes.NewReader(make([]byte, 16)), size: 16}
	for _, off := range []int64{1<<63 - 8, 1<<63 - 1, 1 << 62, 15} {
		if err := f.readAt(make([]byte, 16), off); !errors.Is(err, e.ErrCorrupt) {
			t.Fatalf("off %x: %v", off, err)
		}
	}
	if err := f.readAt(make([]byte, 16), 0); err != nil {
		t.Fatal(err)
	}
}
