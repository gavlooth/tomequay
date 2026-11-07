# Dark page PNGs

`Dark(png []byte) ([]byte, error)` returns a new PNG with unchanged dimensions and alpha. It does not change its input or share returned storage with later calls. `DarkPaper` is a packed RGB constant, `0x101010`: a neutral near-black rather than a blue-tinted black, so grayscale stays neutral and coloured pixels keep their hue. Black becomes `#efefef`. Calls can run concurrently. The package imports only the Go standard library and has no reader or terminal dependencies.

## Mapping choice

Three alternatives were compared on real Poppler renders:

1. RGB inversion: `(255-r, 255-g, 255-b)`.
2. HSL lightness inversion, retaining hue and saturation.
3. RGB inversion followed by a 180-degree HSL hue rotation.

All three received the same final contrast reduction to channel values 16–239. Option 2 is used. For an original channel `c`, its lightness-inverted value is simply:

```text
c' = c + 255 - max(r,g,b) - min(r,g,b)
output = 16 + round(223 * c' / 255)
```

This avoids floating-point colour conversion. The final affine adjustment also preserves hue, with only byte-rounding error, while softening both paper and text. A 256-entry channel table supplies that adjustment, and neutral pixels need only one lookup. The HSL inversion itself preserves saturation; the subsequent contrast adjustment reduces it slightly.

Option 3 is mathematically identical to option 2 in HSL: RGB inversion already rotates hue by 180 degrees and inverts lightness, so the additional rotation restores the original hue. The two outputs differed at **zero pixels** on all three pages. There is no reason to pay for an explicit hue conversion or keep a second implementation.

I viewed the three comparison PNGs and each selected output at its full 1035 × 1340 resolution. On `0703143v1.pdf` page 4, RGB inversion turned the red diagram surfaces cyan and the blue lattice edges orange; the selected mapping kept red, cyan, and blue recognisable, with light equations and fine diagram labels. On `Nielsen_Chuang.pdf` physical page 40 (printed page 12), the options were visually equivalent because the page contains no coloured pixels; body text, italic text, and heading rules remained readable. On `2510.17206v2.pdf` page 8, the selected mapping kept the blue/red curves and their shaded uncertainty regions matched to the legend, unlike the yellow/cyan RGB inversion. Saturated blue remains less bright than the neutral text; hue retention is not a perceptual-brightness equalisation.

For pixels whose largest and smallest original channels differed by more than 40, the mean circular hue errors were:

| Page | Coloured pixels | RGB inversion | Selected mapping |
| --- | ---: | ---: | ---: |
| Diagram, page 4 | 10,149 | 179.973° | 0.028° |
| Text, page 40 | 0 | not applicable | not applicable |
| Colour plot, page 8 | 7,571 | 180.000° | 0.000° |

## Memory and PNG output

Decoded RGBA, NRGBA, Gray, RGBA64, NRGBA64, and Gray16 pixel slices are changed directly. Paletted images only need their palette entries changed. There is no second page-sized image or per-pixel interface conversion on RGB/grayscale inputs. Sixteen-bit PNGs keep their precision and alpha. A shared `png.Encoder` uses `png.BestSpeed` and a concurrent encoder-buffer pool. The output byte buffer belongs to its caller, so it is deliberately not recycled underneath a previously returned PNG.

## Observed performance

Measured on 2026-11-06, linux/amd64, Intel Core Ultra 9 185H, using `/usr/bin/go`. Each real PDF page was rasterised with `pdftoppm -f PAGE -l PAGE -scale-to-x 1035 -scale-to-y 1340 -png -singlefile`. Filtering measurements include decoding, mapping, and encoding, but exclude the Poppler process and disk reads. Temporary benchmark inputs and comparison programs were removed after measurement; no private PDFs are checked in.

| Render | Time per `Dark` call | Original PNG | Dark PNG | Size change |
| --- | ---: | ---: | ---: | ---: |
| `0703143v1.pdf`, page 4 | 30.423 ms | 211,289 B | 243,094 B | +15.05% |
| `Nielsen_Chuang.pdf`, page 40 | 29.003 ms | 289,984 B | 328,688 B | +13.35% |
| `2510.17206v2.pdf`, page 8 | 35.562 ms | 365,369 B | 413,120 B | +13.07% |

The retained synthetic 1035 × 1340 text/colour benchmark measured **21,456,125 ns/op**, **258.56 MB/s**, **5,835,783 B/op**, **23 allocs/op**. Allocations include the PNG decoder's page image; the encoder reuses its compression buffers. The size increase is the measured trade-off for fast PNG encoding, not an extra geometry or colour change. These are local observations, not timing guarantees.

```sh
go vet ./internal/pagefilter/...
go test ./internal/pagefilter/...
go test ./internal/pagefilter/... -run '^$' -bench BenchmarkDark -benchmem
```

Tests cover dimensions, near-black paper, light text, hue retention (including saturated red and translucent colour), grayscale, palette and 16-bit PNGs, invalid input, unchanged input bytes, and independent results during concurrent calls.
