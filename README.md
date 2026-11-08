# tiff-go

Pure Go multi-page TIFF decoder with strict resource limits. No cgo.

```go
f, err := tiff.Open(r, size, tiff.Limits{}) // zero Limits is safe
for i := 0; i < f.NumPages(); i++ {
	p, _ := f.Page(i)
	_ = p.Rows(ctx, func(y int, band []byte) error { /* whole rows, top to bottom */ return nil })
}
```

- Every IFD of a file, classic and BigTIFF, either byte order.
- Strips and tiles decoded incrementally by rows: a single-strip file of any
  legal size streams in bands of at most `Limits.MaxBandBytes` (default 64 MiB).
- None, PackBits, LZW, Deflate, CCITT G3/G4 and JPEG compression; predictor 2.
- Caps checked from the header before allocation: IFDs (10 000, loops
  refused), pixels per IFD, buffered bytes. Breaches return `*LimitError`
  wrapping `ErrLimit`.

Deflate and JPEG come from the private `dobbo-ca/deflate-go` and
`dobbo-ca/jpeg-go` copies, so building needs access to them (see
`docs/development/building.md`). MIT licensed; see `NOTICE`.

Design: `docs/design.md`. Decisions and ceilings: `docs/decisions.md`.
Memory: `docs/guides/memory-limits.md`.
