# Kitty graphics inside tmux

Date: 2026-11-06. **GO for Unicode placeholders in Ghostty 1.4.0 + tmux 3.8. NO-GO for direct placements.** No application code was added.

The throwaway standard-library Go reader passed **20 consecutive complete cycles, 180 actions**, using a single transmitted Poppler PNG. All nine actions in every recorded cycle reached the expected pixels, with no image in another pane/window at the accepted sample. The reader only redrew ordinary text on SIGWINCH; it did not retransmit images or query tmux/Sway layout during these cycles. Selecting another pane correctly leaves the page visible in the reader's inactive pane. Switching to another window hides it.

## Hypothesis and stopping rule, fixed before measurement

Prediction: kitty graphics `a=T,q=2,U=1`, tmux passthrough, and U+10EEEE placeholder cells with image ID in foreground colour and row/column diacritics will follow tmux's text layout through split, selection, window switch, zoom, resize, and exit. Pixels must appear only at the reader's cells. Compare direct `a=T,c=...,r=...` placements without placeholders.

Stopping rule: if the reference `kitten icat --passthrough tmux --unicode-placeholder` fails, inspect passthrough, terminal features, and Ghostty configuration; after one unsuccessful fix, reject kitty and evaluate sixel in foot. The reference succeeded, so this fallback was not needed. The private server starts with passthrough **off** because `-f /dev/null` intentionally ignores the owner's configuration; setting it on was initial setup, not a protocol repair.

## Environment and safe reproduction

Measured: Ghostty 1.4.0-arch1, tmux 3.8, kitten 0.49.2, foot 1.28.0 installed; Sway 1.12. The first reference image and the Go image were also visibly successful in a separate desktop Ghostty. That window was closed when the owner requested no more desktop windows. **All reliability cycles and subsequent measurements below used a separate hidden headless Sway**, not the owner's compositor. No owner's window or tmux server was closed or reconfigured.

Scratch was `/tmp/pdf-kitty-probe-ROMXlr`. Headless setup used this minimal config:

```text
output HEADLESS-1 mode 1280x1024
output HEADLESS-1 bg #202020 solid_color
seat seat0 fallback true
font monospace 10
default_border pixel 0
focus_follows_mouse no
```

```sh
WLR_BACKENDS=headless WLR_LIBINPUT_NO_DEVICES=1 WLR_RENDERER=pixman \
  sway -c /tmp/probe/sway.conf
```

Discover the new Sway IPC socket from its PID (`$XDG_RUNTIME_DIR/sway-ipc.$UID.$PID.sock`), and launch the wrapper with `swaymsg -s "$PROBE_SWAYSOCK" exec /tmp/probe/environment.sh`. That script runs **inside the headless compositor's environment**:

```sh
#!/bin/sh
printenv WAYLAND_DISPLAY SWAYSOCK > /tmp/probe/headless-env
exec env GDK_BACKEND=wayland LIBGL_ALWAYS_SOFTWARE=1 \
  ghostty --gtk-single-instance=false --class=com.probe.gfx \
  --window-width=120 --window-height=40 -e /tmp/probe/start.sh
```

`start.sh` is an executable wrapper, not quoted arguments after `-e`:

```sh
#!/bin/sh
exec tmux -L gfx -f /dev/null new-session -s gfx /tmp/probe/reader
```

The measured headless sockets were `wayland-2` and `/run/user/1000/sway-ipc.1000.957402.sock`; these names are examples, not reusable constants. Set **both** `WAYLAND_DISPLAY` and `SWAYSOCK` from `headless-env` for every subsequent Ghostty/grim/swaymsg command. Use only `tmux -L gfx` for tmux commands. Headless Sway emitted a missing-`swaybg` warning, but its output, Ghostty's software OpenGL rendering, and grim capture worked without a fix attempt. Omitting the background line avoids needing swaybg.

Set `tmux -L gfx set -g allow-passthrough on` and `tmux -L gfx set -g status off`. Measured `terminal-features` were the default `xterm*:clipboard:ccolour:cstyle:focus:title`, `screen*:title`, and `rxvt*:ignorefkeys`; no graphics feature override was needed. Ghostty used the owner's configuration (black background, Terminess Nerd Font 14), without editing it. The hidden terminal was tiled at 1280 × 1024, with 139 × 47 cells of 9 × 21 pixels.

The source was physical page 4 of `~/Documents/code/0703143v1.pdf`:

```sh
pdftoppm -png -f 4 -l 4 -singlefile -scale-to-x 1035 -scale-to-y -1 \
  input.pdf /tmp/probe/page
```

Result: **1035 × 1340 RGB PNG, 210,772 bytes**. Reference command:

```sh
kitten icat --passthrough tmux --unicode-placeholder --transfer-mode stream \
  --image-id 66050 --use-window-size 120,39,1440,1053 \
  --place 30x18@2x3 --align left /tmp/probe/page.png
```

The explicit size supplied to this reference was synthetic; actual cell geometry was measured separately below. `chafa -f kitty --passthrough tmux --probe off --size 30x18 --view-size 139x47 --font-ratio 9/21 page.png` also produced a visibly correct image in the hidden terminal. These were reference display checks; the full cycle run used the Go emitter.

## Exact working bytes

Here `ESC` means byte `0x1b`; all numbers are ASCII decimal. Encode the **PNG bytes** as standard base64. Split that encoded string into payloads of **at most 4096 bytes**, a multiple of four except possibly the final payload. The measured PNG produced 281,032 base64 bytes in 69 chunks.

First kitty APC:

```text
ESC _ G a=T,f=100,t=d,q=2,i=66051,c=30,r=18,C=1,U=1,m=1;<base64 chunk> ESC \
```

Subsequent APCs contain only `m=1;<chunk>`; the last uses `m=0;<chunk>`. There are no spaces between keys in the actual bytes. Wrap **each complete APC separately**:

```text
ESC P tmux; <APC with EVERY inner ESC replaced by ESC ESC> ESC \
```

Thus the start is `\x1bPtmux;\x1b\x1b_G...` and the end is `\x1b\x1b\\\x1b\\`. The 4096 limit is the base64 payload, not the total wrapper length. Never put the entire image in one giant passthrough string. The graphics bytes bypass tmux, but **the placeholder text must not**.

At pane-local row `4+row`, column 3, write `ESC[38;2;1;2;3m` (image ID `0x010203 = 66051`), then for every cell write UTF-8 U+10EEEE, the row diacritic, and the column diacritic. Reset attributes after the grid. Both indices start at zero. The first 30 entries of the protocol's diacritic table, used here, are:

```text
0305 030D 030E 0310 0312 033D 033E 033F 0346 034A
034B 034C 0350 0351 0352 0357 035B 0363 0364 0365
0366 0367 0368 0369 036A 036B 036C 036D 036E 036F
```

Use [the published table](https://sw.kovidgoyal.net/kitty/_downloads/f0a0de9ec8d9ff4456206db8e0814937/rowcolumn-diacritics.txt), not Unicode codepoint arithmetic. [The protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders) also describes a third diacritic for the high image-ID byte and underline colour for placement IDs; neither was required/tested here. Ordinary truecolour text support must preserve the ID colour through tmux.

The virtual rectangle was always 30 × 18 cells; Ghostty fit the page inside it. Its measured white-page rectangle before splitting was x=31..300, y=90..439 (270 × 350 pixels). This run proves movement/redraw of this fixed grid, **not** automatic fit-page resizing of the grid itself. A changed grid size can use `a=p,U=1,i=ID,c=COLS,r=ROWS,q=2` with cached data according to the protocol; that changed-size command was not separately measured.

## Twenty complete cycles

Each cycle used `split-window -h -b -d` to put the other pane **left** of the reader, forcing the image to move right. It then selected the other pane, selected the reader, created a blank window, returned with `previous-window`, zoomed/unzoomed the reader, resized its pane with `resize-pane -L 3`, and killed the other pane. The blank window was removed after each cycle. No reader action on focus/window selection and no tmux/Sway hooks were used.

The Bun observer repeatedly obtained current pane cell geometry and captured the whole hidden terminal with grim PPM until the expected state appeared, waiting at most 2.5 seconds. Visible acceptance required the exact translated page rectangle, no white page pixels elsewhere in the terminal, and every page RGB channel matching the original image within **one** value. Hidden acceptance required no white page pixels anywhere. Initial observer calibration found that translating the otherwise identical page changed 54 RGB channels by one value; strict SHA-256 equality was therefore inappropriate. A white-pixel-count equality also varied by one at the threshold. These were observer corrections, not reader changes; calibration attempts are not included in the following consecutive run.

**Every entry below passed.** Values are milliseconds from before the tmux command to the accepted screenshot; range 18.9–76.6 ms. Screenshots of the reference, moved page, hidden window, restored page, direct-placement failure, and Chafa output were captured and inspected visually.

| Cycle | Split H | Other | Back | New window | Previous | Zoom | Unzoom | Resize pane | Kill other |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 55.7 | 18.9 | 34.2 | 49.5 | 34.7 | 33.1 | 44.4 | 38.5 | 51.8 |
| 2 | 57.7 | 28.6 | 35.9 | 49.4 | 33.7 | 32.6 | 34.9 | 31.4 | 36.7 |
| 3 | 65.1 | 30.2 | 34.0 | 32.1 | 36.5 | 29.8 | 35.6 | 36.9 | 60.7 |
| 4 | 54.9 | 21.0 | 35.1 | 49.1 | 32.8 | 49.7 | 52.1 | 52.7 | 55.6 |
| 5 | 67.7 | 24.5 | 34.7 | 31.8 | 34.8 | 32.2 | 36.1 | 51.6 | 63.8 |
| 6 | 76.6 | 22.9 | 30.5 | 32.1 | 35.6 | 49.8 | 33.9 | 33.2 | 66.0 |
| 7 | 49.3 | 32.7 | 32.2 | 69.6 | 51.4 | 39.1 | 39.9 | 58.5 | 39.7 |
| 8 | 60.7 | 21.6 | 33.0 | 49.0 | 32.8 | 34.4 | 33.8 | 33.9 | 32.1 |
| 9 | 46.6 | 20.1 | 66.3 | 51.5 | 49.9 | 51.6 | 60.7 | 43.9 | 33.0 |
| 10 | 64.0 | 22.3 | 28.4 | 33.4 | 36.0 | 48.3 | 55.0 | 52.2 | 55.1 |
| 11 | 45.4 | 31.8 | 33.5 | 31.6 | 34.4 | 33.7 | 49.1 | 53.0 | 65.5 |
| 12 | 61.7 | 19.6 | 34.0 | 33.6 | 33.9 | 48.4 | 35.7 | 31.6 | 34.9 |
| 13 | 62.4 | 25.0 | 33.0 | 36.2 | 62.0 | 50.1 | 50.7 | 51.6 | 33.4 |
| 14 | 75.7 | 21.8 | 30.5 | 33.0 | 35.4 | 65.8 | 51.1 | 52.2 | 52.8 |
| 15 | 74.7 | 19.0 | 32.1 | 33.2 | 34.3 | 33.7 | 36.8 | 47.4 | 52.9 |
| 16 | 56.2 | 21.7 | 30.9 | 36.7 | 47.3 | 34.6 | 35.4 | 49.5 | 34.0 |
| 17 | 65.2 | 32.4 | 31.3 | 69.3 | 46.5 | 36.6 | 63.8 | 35.7 | 50.4 |
| 18 | 61.6 | 22.3 | 33.6 | 31.5 | 34.9 | 32.8 | 36.9 | 45.4 | 54.0 |
| 19 | 49.9 | 61.2 | 22.5 | 63.9 | 55.3 | 36.9 | 51.5 | 34.9 | 33.4 |
| 20 | 46.0 | 23.1 | 32.9 | 46.4 | 36.9 | 31.8 | 33.3 | 34.7 | 47.2 |

Additional output resize to 1200 × 960 yielded 130 × 44 pane cells and the unchanged, correct page rectangle; restoring 1280 × 1024 returned to 139 × 47. An additional blank-window check outside the table took 907.9 ms before its first accepted hidden screenshot. These are sampled eventual-display results, not proof that every intermediate compositor frame is correct. No settling sleeps were used as a reliability mechanism. Nested tmux, history/copy mode, extremely small panes, font changes, and arbitrary other terminal versions were not tested.

## Direct-placement comparison

Without U=1/placeholders, the image **remained in the left, wrong pane** after the reader moved right. It was still wrong after 2.5 seconds. This also reproduced after explicitly sending cursor positioning through passthrough before the image: direct initial rectangle x=31..300, y=76..453; after split it stayed there while the reader began at column 70. A normal pane-local CUP alone is insufficient to position bypassed graphics reliably because tmux's virtual cursor and the physical terminal cursor differ. Even explicit physical cursor positioning did not solve subsequent movement. The direct variant was rejected at this first split failure rather than run through 20 useless cycles.

## Cell size, timing, and cleanup

- Query the pane PTY with `TIOCGWINSZ`; its nonzero `xpixel/columns`, `ypixel/rows` gave **9 × 21 pixels**. Examples: 139 × 47 cells / 1251 × 987 pixels; after output resize, 130 × 44 / 1170 × 924. Do not divide by zero or assume all tmux/terminal combinations provide pixels.
- `ESC[16t` inside tmux returned `ESC[6;21;9t`. The same query wrapped in passthrough also returned that reply. The reader must parse terminal replies alongside keys, obtain cell pixels at startup and after size/font changes, and use a query wait that expires if no reply arrives. Prefer the pane ioctl when it supplies pixels; the reply is height then width. [Size-query reference](https://sw.kovidgoyal.net/kitty/graphics-protocol/#getting-the-window-size).
- Cold **terminal-image-data** measurement, after `d=I` deletion (not a cold OS file cache): 14.258 ms to read/encode/write the 210,772-byte PNG and draw placeholders; **82.6 ms** from request to first matching grim sample. Poppler rendering time was excluded.
- Clearing the text without deleting data, then redrawing only the same placeholder grid: **0.513 ms** writing text, **48.0 ms** request to matching sample. No PNG/APC image transmission occurred for this replacement. The table's tmux redraws likewise reused the same ID.
- `/proc/.../status` high-water RSS after the experiments: tmux server **7,808 KiB**, Ghostty **548,864 KiB**. These are process-lifetime peaks in the software-rendered headless setup, not incremental image costs or representative hardware-rendered desktop usage. CPU peaks were not measured.
- Erasing placeholders removed the image without a graphics delete. Reader normal exit removed the page within a 44.6 ms sampled check; killing a reader pane removed it within 38.8 ms. A surviving blank pane remained visible, so these were not merely terminal-window-close tests.
- **Image data can outlive the reader.** After normal exit without deletion, writing the same ID's placeholders from the surviving pane displayed page pixels again. Erasing text is therefore visual cleanup, not memory cleanup. Sending the explicit ID deletion made those page pixels disappear; redrawing placeholders after deletion without another upload also produced no page.

For ID-specific cleanup send this complete kitty APC, using the same tmux wrapper:

```text
ESC_Ga=d,d=I,i=66051,q=2;ESC\
```

`a=d,i=ID` alone is **not** the correct ID-specific recipe: omitted `d` defaults to deleting visible placements, and virtual placeholders need the ID selector. `a=d,d=i,i=ID` removes that ID's placements while retaining data; uppercase `d=I` also frees its image data. Send `d=I` when evicting a cached page and on orderly reader exit, for every reader-owned ID, before leaving its alternate screen. Do not delete all terminal images: other panes may own them. On abrupt pane kill the text/image vanishes automatically, but process cleanup cannot be relied on to free transmitted data.

## Recommended `internal/imageout` design

Use Go standard library only, writing through the reader's existing terminal writer/input loop. No overlay child, Wayland integration, Sway queries, focus listeners, or tmux layout hooks belong in this package.

```go
New(w io.Writer, tmux bool) *Output
(out *Output) SendPNG(id uint32, png io.Reader, columns, rows int) error
(out *Output) Place(id uint32, columns, rows int) error // cached virtual placement
(out *Output) WriteCells(id uint32, x, y, columns, rows int) error
(out *Output) Delete(id uint32) error
```

`SendPNG` streams base64 chunks and creates the virtual placement. `WriteCells` writes ordinary pane-local text, clipping to the available cells; `Place` changes the cached virtual rectangle when the reader deliberately changes image size. Keep cell-size querying/reply parsing in the terminal input layer, not a second competing stdin reader. Serialize all image/text writes with the main draw loop. A reusable 3072-byte input buffer produces 4096-byte base64 payloads without reading/copying the whole PNG.

IDs are terminal-global, not pane-local. Assign nonzero IDs per cached rendered page, retain them while cached, and explicitly delete before reuse/eviction/exit. A random 24-bit starting range reduces cross-reader collisions but does not guarantee their absence; full 32-bit IDs with the protocol's third diacritic are an available unmeasured extension. Do not silently reuse another page's ID. Cache misses send PNG once; pane/window redraws emit only cells. Normal shutdown erases cells, deletes owned IDs, and restores the screen. The tested layout path needs neither hide/show graphics commands nor image retransmission on pane focus.

The private tmux server, Ghostty, headless Sway, and scratch files were removed after measurement. This document is the retained result; no throwaway emitter was added to the repository.
