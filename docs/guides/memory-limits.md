# Memory limits

| Cap | Field | Default | Checked |
|---|---|---|---|
| IFDs in the chain | `MaxIFDs` | 10 000 | `Open`, while walking; loops are `ErrCorrupt` |
| Pixels per IFD | `MaxPixels` | 178 956 970 | `Page`, from Width x Height, before any allocation |
| Decoded bytes buffered | `MaxBandBytes` | 64 MiB | `Rows` |

Peak memory of one `Rows` call, beyond what the caller keeps:

- Strips: one band (at most `MaxBandBytes`) plus the codec state: LZW about
  20 KiB, Deflate its window, PackBits and none a 4 KiB reader.
- Tiles: one tile row (at most `MaxBandBytes`) plus one tile row of scratch.
- JPEG: the compressed chunk, plus jpeg-go's own working set for one band
  (RGBA, 4 bytes per pixel, so the band is `MaxBandBytes / 4w` rows).

A row wider than `MaxBandBytes` is refused. A smaller `MaxBandBytes` trades
more bands for less memory; the pixels cap is independent and bounds the
decoded image, not the buffer.
