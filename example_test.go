package tiff_test

import (
	"bytes"
	"context"
	"fmt"

	tiff "github.com/dobbo-ca/tiff-go"
	"github.com/dobbo-ca/tiff-go/internal/fixture"
)

func Example() {
	p := fixture.Page{W: 4, H: 2, SPP: 1, BPS: 8, Photometric: tiff.BlackIsZero, Compression: 5}
	b := fixture.File{Pages: []fixture.Page{p, p}}.Bytes()

	f, err := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
	if err != nil {
		panic(err)
	}
	for i := 0; i < f.NumPages(); i++ {
		pg, err := f.Page(i)
		if err != nil {
			panic(err)
		}
		rows := 0
		_ = pg.Rows(context.Background(), func(y int, band []byte) error {
			rows += len(band) / int(pg.Info().RowBytes)
			return nil
		})
		fmt.Println("page", i, "rows", rows)
	}
	// Output:
	// page 0 rows 2
	// page 1 rows 2
}
