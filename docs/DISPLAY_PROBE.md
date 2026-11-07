# Display probe: Sway / Alacritty / tmux

Date: 2026-11-06. Scope: PLAN.md, step 1 only; no reader implementation.

## Verdict: NO-GO for the current overlay design

The PLAN.md hypothesis is that a Poppler page image can stay crisp and correctly placed inside an Alacritty tmux pane through pane changes, window changes, and terminal resizing, without stale images.

**The image quality passed; placement and lifecycle reliability did not. Stop the overlay design at this gate rather than treating a successful static page as approval to build the reader.** In this session:

- A full-width page remained full-width after a horizontal tmux split, covering both panes.
- Selecting the other pane did not remove the page.
- An explicit pane-selection removal hook fixed one isolated switch, but a repeated split/select/add sequence lost its newly added page to a delayed `window-layout-changed` message. The repeated test stopped at its first failure; it did not produce ten successful cycles.
- Window hiding/restoration worked after fixing the temporary-socket environment. Terminal resize removed the old image, and explicit removal and normal process termination also worked.

These observations do not establish that every future integration is impossible. They establish that reliable event ordering and pane cleanup have **not** been proved here. A revised display-only experiment would need to demonstrate them before this verdict can change. Arbitrary settling sleeps are not evidence of a reliable implementation. Zathura remains the working comparison, not an in-terminal substitute accepted by this probe.

## Environment and installation

Measured versions: Überzug++ 2.9.10; Alacritty 0.17.0; tmux 3.8; Sway 1.12; Poppler `pdftoppm` 26.10.0; chafa 1.18.3; Zathura 2026.10.23 with its `pdf-mupdf` plugin. `grim`, `socat`, `wtype`, `gdbus`, Bun, and `/usr/bin/time` were available.

The lead installed the Arch package during the probe after confirming passwordless sudo worked. `/usr/bin/ueberzugpp --version` succeeded. The originally suggested non-root installation workarounds were therefore unnecessary and were not attempted. There is no missing-installation blocker. The owner can reproduce installation with:

```sh
sudo pacman -S --needed ueberzugpp
```

The installed binary warned that the optional libvips `vips-openslide.so` module could not load because `libopenslide.so.1` was absent. PNG loading and display still worked. This was not suppressed or mistaken for the placement failure.

The probe launched its own Alacritty and `tmux -L probe -f /dev/null`, using separate Sway workspaces. It did not change the owner's tmux options or hooks. The real display used for the probe was HDMI-A-1, a 1080 × 1920 portrait output, Sway scale 1 and transform 270. Another output, DP-1, was 2560 × 1440; it was not the geometry used for these timings.

### Measured terminal geometry

Sources were `swaymsg -t get_tree -r`, `tmux -L probe display -p`, and terminal replies collected inside the probe pane. Both ordinary queries and tmux-passthrough queries were tried.

| Measurement | Observed value |
| --- | --- |
| Sway Alacritty rectangle | x=3640, y=507, width=1080, height=1854 |
| Sway `window_rect` | x=2, y=0, width=1076, height=1852 |
| tmux client | 117 columns × 87 rows |
| tmux pane, before split | 117 columns × 86 rows; bottom status row excluded |
| `ESC [ 16 t` reply | `ESC [ 6 ; 21 ; 9 t`: cell height 21 px, width 9 px |
| `ESC [ 14 t` reply through tmux | `ESC [ 4 ; 1806 ; 1053 t`: pane pixels |
| Passthrough `ESC [ 14 t` reply | `ESC [ 4 ; 1827 ; 1053 t`: terminal pixels, including tmux status row |
| Requested page rectangle | x=1, y=2, max_width=115, max_height=82 cells |
| Available image pixels at that request | 1035 × 1722 |
| Sway rectangle of displayed diagram | x=3662, y=573, width=1035, height=1340 |

The actual diagram rectangle was inside the Alacritty rectangle. The two panes after `split-window -h` were each 58 × 86 cells, starting at columns 0 and 59. The previously displayed 1035-pixel-wide image did not shrink to the new left pane.

## PDFs and Poppler measurements

Read-only source files:

- `~/Documents/code/Nielsen_Chuang.pdf`: 704 pages; physical PDF page 40, printed page 12, “Introduction and overview / Future directions,” for dense prose. Physical pages 40–49 were also used for ten successive changes.
- `~/Documents/code/0703143v1.pdf`: 20 pages, Raussendorf, Harrington, and Goyal, *Topological fault-tolerance in cluster state quantum computation*. Physical page 4 contains lattice/code diagrams and correlation surfaces, plus equations and text. The rendered page was visually inspected before choosing it.

Each timing used a fresh `pdftoppm` process. Wall time was measured with Bun's monotonic `performance.now()` around process execution, including spawning, PDF reading, rendering, PNG encoding, and writing under `/tmp`. Each row has one first invocation followed by ten invocations; the median is the mean of the fifth and sixth sorted repeat times. There was no concurrent render worker in the probe.

**“First” means cold renderer process, not cold disk cache.** No system cache was dropped. Metadata/text inspection and other trials had already touched these PDFs. Disk-cold latency is unmeasured. PNG sizes are exact bytes, not an estimate of decoded memory.

Base command, with page `N` and the size arguments from the table:

```sh
pdftoppm -png -f N -l N -singlefile <size-arguments> input.pdf /tmp/probe/page
```

### Render times at the measured pane size

| Page | Size arguments | PNG pixels | PNG bytes | First ms | Median of 10 ms | Repeat range ms |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| Nielsen p40 | `-r 116` | 1027 × 1360 | 290739 | 194.4 | 193.1 | 189.9–207.7 |
| Diagrams p4 | `-r 121` | 1029 × 1331 | 215136 | 178.1 | 163.3 | 157.1–175.1 |
| Nielsen p40 | `-scale-to-x 1035 -scale-to-y -1` | 1035 × 1371 | 297637 | 197.0 | 199.6 | 196.3–220.4 |
| Diagrams p4 | `-scale-to-x 1035 -scale-to-y -1` | 1035 × 1340 | 210772 | 163.7 | 160.3 | 157.5–172.0 |
| Nielsen p40 | `-scale-to-x 1035 -scale-to-y 1722` | 1035 × 1722 | 339336 | 244.8 | 239.2 | 235.2–251.7 |
| Diagrams p4 | `-scale-to-x 1035 -scale-to-y 1722` | 1035 × 1722 | 251824 | 204.9 | 201.2 | 188.6–210.3 |
| Nielsen p40 | `-scale-to-x -1 -scale-to-y 1722` | 1301 × 1722 | 394412 | 274.8 | 273.0 | 268.9–282.9 |
| Diagrams p4 | `-scale-to-x -1 -scale-to-y 1722` | 1331 × 1722 | 284248 | 218.2 | 220.9 | 217.8–229.1 |

Important size findings:

1. Supplying both positive `-scale-to-x` and `-scale-to-y` produced exactly those two dimensions and distorted these pages. It is **not** an aspect-preserving fit-page command. A correct fit calculation must choose the limiting dimension and use `-1` for the other, or calculate two aspect-preserving dimensions itself.
2. Height-only rendering exceeded this pane's width for both pages. It would need horizontal scrolling or a different fit choice.
3. `pdfinfo` reports Nielsen page 40's CropBox dimensions as 482.4 × 741.6 points, but `pdftoppm` without `-cropbox` uses the 637.2 × 843.84-point MediaBox. An initial 154-DPI trial based on the smaller reported page size therefore produced an oversized 1363 × 1805 PNG: 421690 bytes, first 282.3 ms, median 277.2 ms, range 272.4–284.9 ms. This trial was retained as a sizing warning, not presented as a pane-fitting render. The corrected 116-DPI result is in the table. Using `-cropbox` would be a different, unmeasured render policy.

### Image appearance and navigation latency

To observe visible changes rather than only JSON write completion, the probe captured a 1 × 1 PPM pixel with `grim` at desktop x=3700, y=600. This is black without the image and white inside its page margin. Each add was preceded by a remove and confirmed black pixel. The timer stopped on the first screenshot confirming white; removal stopped on black. This measures sampled appearance/disappearance, **not** complete page-presentation acknowledgement or sub-frame latency. It includes screenshot-process overhead and compositor timing. Ordinary empty/removed captures took approximately 14–25 ms in these trials.

| Operation | Samples | Median ms | Range ms |
| --- | ---: | ---: | ---: |
| Add an already-rendered PNG, alternating text/diagram | 10 | 133.8 | 125.1–141.0 |
| Remove the preceding image | 10 | 16.4 | 13.9–24.6 |
| Render Nielsen p40–49 at width 1035, add, observe white pixel | 10 | 330.5 | 230.7–373.1 |

The ten physical pages' end-to-end times in order were 373.1, 324.8, 337.8, 326.2, 328.9, 332.1, 326.0, 230.7, 334.4, and 333.3 ms. Render portions ranged from 155.5 to 214.6 ms; add-to-sample portions ranged from 75.2 to 171.9 ms. These are measurements of serial remove/render/add, not a reader with prefetch, not direct replacement of a still-visible image, and not ten open tabs. Reusing an existing PNG avoids the Poppler work; any more elaborate cache benefit is an assumption.

## Überzug++ invocation and protocol

The foreground process that actually displayed the inspected pages was launched **inside the tmux pane**, with its terminal stdin left open:

```sh
export UEBERZUGPP_TMPDIR=/tmp/pdf-display-probe-Ngr4YO
/usr/bin/time -v -o "$UEBERZUGPP_TMPDIR/overlay-time.txt" \
  /usr/bin/ueberzugpp layer \
  --pid-file "$UEBERZUGPP_TMPDIR/overlay.pid" --output wayland
```

The directory above is the actual run's scratch directory and was deleted afterwards; create a fresh directory for reproduction. A small temporary Bun harness inherited the pane's terminal, captured the size-query replies, and launched this foreground command with inherited stdin/stdout/stderr. It did not feed image JSON through the terminal. The real foreground image process PID was 793327 and its socket was:

```text
/tmp/pdf-display-probe-Ngr4YO/ueberzugpp-793327.socket
```

**Foreground detail:** `--pid-file` did not rewrite the earlier daemon PID file in this mode. The foreground PID/socket were obtained from process ancestry and the log. An application launching the foreground binary should use that child's actual PID, not a stale PID file. A separate initialization trial with `--no-stdin --pid-file ... --output wayland` successfully created a daemon and socket, but was not the process used for the visual/memory results.

The tmux probe had `allow-passthrough on`. Query passthrough used `ESC P tmux ; ESC ESC [ 16 t ESC \\` and the corresponding `14t` query. This is terminal-query passthrough; the Wayland pixels themselves are not emitted as terminal image escape sequences.

Send newline-terminated JSON over a Unix stream connection. The probe used Bun's `node:net`, opened one connection per command, wrote JSON plus newline, and closed it. Equivalent `socat` commands are:

```sh
socket="$UEBERZUGPP_TMPDIR/ueberzugpp-793327.socket"
printf '%s\n' \
  '{"action":"add","identifier":"page","path":"/absolute/path/diagram-width.png","x":1,"y":2,"max_width":115,"max_height":82}' \
  | socat - "UNIX-CONNECT:$socket"
printf '%s\n' '{"action":"remove","identifier":"page"}' \
  | socat - "UNIX-CONNECT:$socket"
```

Coordinates and maximum dimensions are **cells relative to the originating pane**, not desktop pixels. On the split left pane the successful manually sized request used `max_width:56`. `remove` with the same identifier visibly removed the image. No response acknowledging visual completion was observed or used.

### tmux environment and hook findings

Überzug++ installed tmux hooks invoking commands such as:

```text
ueberzugpp tmux session-window-changed 793327
ueberzugpp tmux client-session-changed 793327
```

The first window-switch trial failed because the temporary socket directory was set only on the image process, not in the tmux server environment. The helper consequently did not reach the temporary socket. After:

```sh
tmux -L probe set-environment -g UEBERZUGPP_TMPDIR /tmp/pdf-display-probe-Ngr4YO
```

switching to the other tmux window removed the image and returning restored it. For reproduction, export the variable **before launching the dedicated tmux server**, or explicitly set the server environment as above. This initial configuration failure is separated from the remaining pane/layout failures.

An additional `after-select-pane` hook ran this removal command using the same socket:

```sh
printf '%s\n' '{"action":"remove","identifier":"page"}' | socat - "UNIX-CONNECT:$socket"
```

One isolated select-pane trial passed with that hook. Similar explicit removal hooks were tried for `client-resized`, `after-split-window`, and `after-resize-pane`. They were temporary experiments, not a proposed permanent implementation. They do not prove event ordering: the repeated test below still failed.

### Observed placement and cleanup results

All screenshots listed here were captured with `grim` and visually inspected. The raw screenshots lived only in scratch and were removed; the table records their observations, not links to files that no longer exist.

| Test / screenshot label | Observation |
| --- | --- |
| `overlay-refocus`, `text-overlay` | Sharp PDF text, equations, colored diagram lines, and labels at full pixel resolution. Static placement fit the requested pane rectangle. |
| Initial hidden-workspace add | On returning to the probe workspace, the first page was absent. Explicit remove/add while focused produced the inspected image. No reliable background-add/restoration result is asserted. |
| `split-auto` | Existing 1035-pixel-wide image still covered both panes after split. Sway confirmed its unchanged 1035 × 1340 rectangle. **Failure.** |
| `pane-other` | After manually fitting the image to the left pane, selecting the right pane left the image visible in the left pane. It did not cover the right pane in this fitted case, but failed the requested removal-on-pane-switch test. |
| `removed`, `pane-managed` | Explicit remove and an isolated `after-select-pane` removal hook left the panes clear. |
| `window-other` | Initial window switch retained the image over “OTHER TMUX WINDOW” because the helper lacked the scratch-directory environment. |
| `window-fixed`, `window-return` | With the server environment corrected, another tmux window was clear and returning restored the page. |
| `resize-auto` | Changing the Alacritty to an 800 × 1000 floating size left the old page absent; panes became 42/43 × 44 cells. Automatic removal passed, but automatic resize-and-redraw was not established. |
| `resize-managed` | An additional resize to 900 × 1100 with an explicit removal hook also left no stale image. |
| `repeat-failure` | Attempted ten-cycle split/select/add test failed before its first complete cycle: the reader pane was selected, but its newly added page disappeared. No ten-cycle pass is reported. |
| `overlay-exit` | SIGTERM to the image process removed its displayed page. `/usr/bin/time` reported exit status 0. SIGKILL cleanup was not tested. |

The repeated failure has a precise log sequence (local wall time):

```text
18:58:25.646  remove page
18:58:25.655  remove page
18:58:25.656  add page, x=1 y=2 max_width=56 max_height=82
18:58:25.676  opencv loading diagram-width.png
18:58:25.733  tmux hook window-layout-changed
```

The subsequent screenshot showed no page, and 30 consecutive sample captures never found its white pixel. **[INFERENCE]** The delayed layout event invalidated the newer display request. The externally visible disappearance is measured; attributing its internal cause specifically to that final hook is an inference from the ordering. This is why the one successful cleanup hook is insufficient for a GO verdict.

## Chafa and Zathura comparison

### Chafa at the same terminal size

Command:

```sh
chafa --probe off --format symbols --colors full \
  --font-ratio 9/21 --size 115x82 page.png
```

The diagram was displayed in another window of the same probe tmux session and inspected. The comparison used the same 1035-pixel-wide input PNGs. The explicit font ratio matches the measured 9 × 21-pixel cells.

| Input | First ms | Median of 10 ms | Output bytes |
| --- | ---: | ---: | ---: |
| Text page | 33.0 | 32.4 | 107523 |
| Diagram page | 24.5 | 33.4 | 92721 |

These times are conversion to captured terminal output, excluding Poppler rendering and terminal display completion. At the actual size, chafa preserved broad layout and colored areas, but ordinary paragraph text and the diagram's mathematical labels were unreadable character patterns. The overlay had clearly readable labels and prose where chafa did not. Symbols are a useful preview, not an equivalent reading surface here.

### Zathura reference

Zathura was launched with separate configuration, data, and cache directories under scratch, on the same 1080 × 1854 Sway window rectangle:

```sh
/usr/bin/time -v -o /tmp/probe/zathura-time.txt \
  zathura -c /tmp/probe/zathura-config -d /tmp/probe/zathura-data \
  --cache-dir /tmp/probe/zathura-cache -P 40 \
  "$HOME/Documents/code/Nielsen_Chuang.pdf"
```

One startup took **294.9 ms to Sway's new-window event**. This is a measured window-creation time, not a measured first-complete-page time: the event initially had no assigned rectangle, so the immediate screenshot attempt failed and a later capture was used for visual inspection. No startup median or disk-cold startup number is asserted.

Ten page jumps used the real application's D-Bus method, not a renderer substitute:

```sh
gdbus call --session --dest org.pwmt.zathura.PID-822252 \
  --object-path /org/pwmt/zathura \
  --method org.pwmt.zathura.GotoPage 399
```

The page argument is zero-based. Indices were 399, 599, 99, 299, 499, 199, 649, 49, 349, and 39. Method returns were `(true,)`. Latency stopped on a changed 600 × 600 screenshot region with more than 1000 dark pixels, avoiding counting a wholly blank redraw as success. This is still a sampled visible-change estimate, not an application-provided fully rendered event.

- D-Bus call completion: 5.1–13.0 ms.
- Visible-change estimate: **median 63.8 ms**, range 41.3–88.0 ms.
- Individual visible times: 74.2, 65.4, 68.0, 41.3, 65.8, 50.9, 88.0, 49.6, 62.2, 52.1 ms.

The screenshots showed immediately useful, sharp prose. Subjectively this was a more responsive reading reference than render/remove/add. Zathura's default continuous/zoomed layout showed larger text and portions of adjacent pages; the overlay showed an entire MediaBox page. The outer window sizes matched, **not** the exact zoom/cropping policy, so the comparison is a practical reference rather than an equal-work renderer benchmark.

## Peak memory

Measured with `/usr/bin/time -v`, in KiB of maximum resident set size:

| Process / workload | Peak RSS KiB | Exit status |
| --- | ---: | ---: |
| Poppler, Nielsen p40, `-png -r 116` | 27816 | 0 |
| Überzug++ foreground, all display trials and p40–49 updates | 390784 | 0 |
| Chafa, diagram at 115 × 82 symbols | 35808 | 0 |
| Zathura, startup and ten random jumps in Nielsen | 788064 | 0 |

The overlay measurement includes its approximately 6.5-minute process lifetime and default image-loading/cache behavior. It is not only the PNG's storage size. Zathura's peak covers the whole jump workload, not only startup. These are separate process peaks, not simultaneous totals, and do not include the owner's desktop or provide a ten-tab application-memory measurement. The PLAN.md decoded-page/cache calculations remain estimates; this probe does not validate a 128 MiB global cache target.

## Cleanup and retained result

The temporary directory `/tmp/pdf-display-probe-Ngr4YO`, Bun harness, PNGs, screenshots, sockets, logs, and isolated Zathura state were deleted. Two image-cache PNGs generated by the probe under `~/.cache/ueberzugpp` were also removed. The image process and Zathura were terminated, and only the dedicated `tmux -L probe` server was killed; its Alacritty and sleeping pane processes exited. A final process/window inspection found none of the probe PIDs or windows, and the scratch directory no longer existed. The original focused window was restored. The owner's default-server session names/window counts were unchanged before and after cleanup.

The retained deliverable is this report. No Go app code, app dependencies, package tests, or reader features were added. No Go build, vet, test, or formatting command was relevant or run for this display-only assignment.

## References

- [PLAN.md](PLAN.md), display hypothesis and stop criterion.
- [Überzug++ README](https://github.com/jstkdng/ueberzugpp): Wayland/tmux support, cell coordinates, foreground/daemon options, temporary directory, Unix-socket JSON protocol.
- [Überzug++ tmux implementation](https://github.com/jstkdng/ueberzugpp/blob/master/src/tmux.cpp): helper hooks and pane/window queries; consulted to interpret the observed behavior.
- [Poppler pdftoppm manual](https://manpages.debian.org/stable/poppler-utils/pdftoppm.1.en.html); the installed `pdftoppm -h` also documented the size and CropBox options used above.
