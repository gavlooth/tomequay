# Poppler PDF backend

`FindTools` resolves `pdfinfo`, `pdftoppm`, and `pdftotext` from `PATH`. Install `poppler` on Arch Linux or `poppler-utils` on Debian/Ubuntu. The package uses only Go's standard library and does not write to PDF files or create image/text sidecars.

- `Tools.Info` reads page count, title, encryption status, the unrotated crop-box dimensions of page 1 in PDF points, and its `PageRotationDegrees`.
- `Tools.RenderPage` returns one PNG for a 1-based page. Positive pixel dimensions describe a fit box, preserving aspect ratio and page rotation. `Zoom: 1` fits inside that box; `Zoom: 2` doubles the fitted image. Zoom must be explicitly positive and finite. Mixed page sizes use the requested page's dimensions, not page 1's dimensions. Raster rounding can change the other dimension by less than one pixel. When both `RenderRequest.CropWidth` and `CropHeight` are positive, `CropX`, `CropY`, `CropWidth`, and `CropHeight` select a window in the full scaled page's pixel coordinates. The window is intersected with the scaled page and only that window is rendered using Poppler's `-x`, `-y`, `-W`, and `-H`. An empty intersection is an error; a zero crop leaves full-page rendering unchanged.
- `ScaledPageSize(info DocumentInfo, r RenderRequest) (w, h int)` is a pure function returning the full scaled page's pixel dimensions, ignoring cropping. It starts no processes and returns `(0, 0)` for missing/invalid page dimensions or invalid pixel dimensions/zoom. Its calculation is shared with rendering, including rotation and raster rounding. Pass metadata for the requested page for exact dimensions; `Tools.Info` describes page 1, so using it for other pages is an approximation when their size or rotation differs.
- `Tools.PageText` returns one page's UTF-8 text without a form-feed separator.
- `Tools.SearchPages` returns 1-based page numbers for case-insensitive substring matches within each page. An empty query or a PDF without extractable text returns an empty result. It does not perform OCR, join words split across lines, or search across page breaks.
- Use `errors.Is` with `ErrPasswordRequired`, `ErrWrongPassword`, `ErrInvalidPDF`, and `ErrPageOutOfRange`. Poppler's diagnostic text is retained on errors. Other execution and I/O failures are returned as errors without being relabeled as invalid PDFs.

Every process receives separate arguments and `LC_ALL=C`. Input paths are made absolute, including filenames starting with `-`; no shell is involved. Rendering writes PNG output directly to stdout with `-png -singlefile -cropbox` and no output prefix. The crop box matches the visible page dimensions reported by `pdfinfo`, even when the media box includes extra margins. One scale axis is selected and the other is set to `-1`, preserving the aspect ratio; scale axes are adjusted for rotation. Metadata is read before rendering or extracting a page to reject invalid page numbers. No metadata or image cache is kept here.

The installed Poppler 26.10.0 help exposes `-upw` but no password-file or password-descriptor option. Consequently, supplied passwords appear in child-process arguments and may be visible to other users through process inspection. The package does not log passwords or include command arguments in its errors. An empty password means no supplied password; a rejected empty password returns `ErrPasswordRequired`.

Context cancellation kills the active direct Poppler child and waits for it to exit before returning. No image or text output is returned on cancellation or process failure. Successful Poppler commands may emit syntax warnings; those warnings do not turn a successful operation into an error. Calls are independent and a `Tools` value can be shared between goroutines.

## Checks

```sh
go vet ./internal/pdf/...
go test ./internal/pdf/...
PDF_REAL_SMOKE=1 go test ./internal/pdf/... -run TestNielsenChuangSmoke -v
```

Integration tests generate small PDFs locally and skip with a reason if Poppler is absent. Encryption tests use `qpdf` when installed and otherwise explicitly skip. The opt-in smoke test reads `~/Documents/code/Nielsen_Chuang.pdf`, renders page 300 into a 1200 × 1600 fit box, searches for `qubit`, and logs timings.

## Observed run (2026-11-06)

On the development machine with Poppler 26.10.0, `go vet ./internal/pdf/...` succeeded and `PDF_REAL_SMOKE=1 go test ./internal/pdf/... -v` passed all cases, including qpdf-generated encryption and rotated/cropped-page fits. The package test run took 3.784 seconds.

For the 704-page Nielsen–Chuang PDF:

| Operation | Observed time | Result |
| --- | --- | --- |
| `Info` | 10.312161 ms | 704 pages; empty title; page 1 is 482.4 × 741.6 points |
| `RenderPage`, page 300, 1200 × 1600 fit box | 300.54116 ms | 1041 × 1600 PNG; 400,827 bytes |
| `SearchPages("qubit")` | 2.611096567 s | 332 matching pages; first page 5, last page 704 |

The deterministic cancellation test killed and reaped an active renderer subprocess in 420.093 microseconds and returned no partial image. These are single-run observations, not performance guarantees. The real document also exposed the distinction between media-box and crop-box dimensions; rendering now explicitly uses the crop box, with generated regression cases for both unrotated and rotated cropped pages.

The crop/sizing extension adds regression cases for 2× zoom, top-left quarter windows, windows crossing page edges, negative crop origins, entirely off-page windows, full-page output equivalence, rotated pages, and equality between `ScaledPageSize` and uncropped PNG dimensions.
