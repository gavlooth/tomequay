# Reader-owned display probe

Date: 2026-11-06. This follow-up isolates reader-owned image placement from the stock Überzug++ tmux hooks. No application code was added.

## Verdict: NO-GO for adopting the overlay in the reader

**The serialized reader-owned approach passed 20 consecutive requested tmux cycles: 180 actions, zero mismatches.** This is materially better than the stock-hook result in [DISPLAY_PROBE.md](DISPLAY_PROBE.md). However, a later workspace/focus change exposed a different stale-image case: the terminal was hidden on its Sway workspace while subsequent tmux events created an image on the other output. tmux still reported the client as focused. A single fix attempt added Sway notifications and a Sway visibility/focus query to the same event loop; its full-cycle reliability was **not proved** before the experiment was stopped.

The owner then chose Ghostty with native kitty graphics and explicitly stopped further overlay experiments. This verdict means **do not adopt the overlay from this evidence**, not that the successful 20-cycle result did not occur. SIGKILL cleanup and the revised loop's 20-cycle gate remain unmeasured. No production overlay package or API is proposed as proved.

## Hypothesis and stopping rule, chosen before measurement

Hypothesis: a single serialized reader loop can own every image add/remove, consume terminal focus reports, SIGWINCH, keys, and notification-only tmux hooks, query current pane geometry, and avoid stale pages through splitting, selecting panes, switching windows, zooming, resizing, killing the other pane, and reader exit.

Predicted signature: grim finds the white page margin inside the expected reader-pane image rectangle when that pane is active in the visible window, and background otherwise. Require 20 consecutive full cycles of split, select-other, select-back, next-window, previous-window, zoom, unzoom, resize, kill-other-pane, with zero mismatches. Alternate horizontal and vertical splits.

Kill criterion: a systematic miss requiring a settling sleep, or more than one mismatch after one fix attempt for a failure class, means NO-GO. There were no settling sleeps in the measurement loop. The later external-focus failure received one code change, but not a completed post-change reliability run; the final stop was the owner's change of direction, not an assertion that the revised code passed.

## Environment and isolation

Measured versions: Überzug++ 2.9.10, Alacritty 0.17.0, tmux 3.8, Sway 1.12, chafa 1.18.3. The first probe already established working grim and passwordless sudo. This probe used a separate Alacritty with app ID `owned-probe2`, a separate Sway workspace named `owned-probe2`, and `tmux -L probe2 -f /dev/null`. The owner's tmux server, options, hooks, and panes were not changed.

Scratch was `/tmp/pdf-owned-probe-r8ULBN`. It held a throwaway standard-library Go host/reader, a Bun measurement harness, source inspection material, and PNGs. The PDF was physical page 4 of `~/Documents/code/0703143v1.pdf`, rendered with:


```sh
pdftoppm -png -f 4 -l 4 -singlefile \
  -scale-to-x 1035 -scale-to-y -1 input.pdf /tmp/probe/page
```

The PNG was 1035 × 1340 pixels. Cell size was 9 × 21 pixels. The original tiled Alacritty rectangle was x=3640, y=507, width=1080, height=1854. Its initial page rectangle was x=3662, y=573, width=1035, height=1340. Repeated cycles used a floating terminal, alternating requested outer sizes 900 × 1100 and 1000 × 1300; Sway reported content rectangles 900 × 1073 and 1000 × 1273. tmux sizes changed accordingly. The tmux status row was excluded from pane height.

## Preventing the stock hooks from competing

`ueberzugpp layer --help` has no no-tmux option. Inspection of the exact v2.9.10 source showed:

- `tmux::is_used()` tests `TMUX_PANE`, not merely `TMUX`. Removing only `TMUX` is insufficient.
- With `TMUX_PANE` absent, Überzug++ registers no tmux hooks and applies no pane offset.
- Without tmux discovery, Sway terminal matching follows the image process parent's process ancestry. An ordinary daemonized tmux server need not have the attached Alacritty in that ancestry.

The measured solution was an outside-tmux host under the dedicated Alacritty. It removed both tmux environment variables, set `UEBERZUGPP_TMPDIR` **before** starting the dedicated tmux server, then started:


```sh
env -u TMUX -u TMUX_PANE UEBERZUGPP_TMPDIR=/tmp/probe \
  ueberzugpp layer --output wayland --no-cache
```

The Go host kept the child's stdin pipe open and did not write image requests to it. It then started its dedicated tmux session with the reader in a pane. The reader inherited the image socket path and was the only sender of image JSON. This preserves the Alacritty ancestry without any stock tmux hook installation. Inspection of the dedicated server's hooks showed the reader's notification commands, not `ueberzugpp tmux ...` commands.

**Integration restriction:** this does not prove that a reader started inside an arbitrary pre-existing tmux pane can spawn an environment-cleared Überzug++ child and still discover its terminal. The working arrangement requires the outside-tmux host/launcher. A production design would need to account for that extra process and launch path rather than silently assuming an ordinary pane-local child works.

The optional libvips openslide module again warned about absent `libopenslide.so.1`. PNG display worked; the warning was not treated as the placement failure.

## Measured loop and protocol

The pane reader enabled `ESC [ ? 1004 h` and set `focus-events on` on the private tmux server. Its input goroutine only queued bytes; its signal goroutine only queued SIGWINCH/exit notifications. A Unix listener queued hook notification lines. All geometry queries and image writes happened in one loop.

Notification-only hooks covered `after-split-window`, `after-select-pane`, `session-window-changed`, `window-layout-changed`, `client-resized`, `after-resize-pane`, `pane-mode-changed`, and `after-kill-pane`. Example shape:


```sh
run-shell -b 'printf "window-layout-changed\n" | socat - UNIX-CONNECT:/tmp/probe/events.socket'
```

Each received event incremented a sequence number and queried current state, rather than trusting an old hook payload:


```sh
tmux -L probe2 display-message -p -t "$TMUX_PANE" \
  '#{pane_left} #{pane_top} #{pane_width} #{pane_height} #{pane_active} #{window_active} #{window_zoomed_flag} #{pane_in_mode}'
```

The first loop drew only for pane_active=1, window_active=1, pane_in_mode=0. Focus-out immediately requested removal. If current state matched the previous state, delayed duplicate events did not redraw. There were no asynchronous PDF results in this probe: the sequence number identified loop iterations, and synchronous querying prevented older query results from finishing after newer ones. This is **not** a test of cancellation or generation checks for concurrent renders.

On a changed state, one Unix stream connection carried newline-separated JSON, remove first, then add if visible:


```json
{"action":"remove","identifier":"page"}
{"action":"add","identifier":"page","path":"/tmp/probe/page.png","x":1,"y":2,"max_width":115,"max_height":82}
```

The reader calculated x=pane_left+1, y=pane_top+2, max_width=pane_width−2, max_height=pane_height−4. Because the image child had no tmux environment, these were terminal-relative cells, not pane-relative cells. Each update used the same pre-rendered PNG. No protocol reply acknowledging visual presentation was observed or assumed.

### Pixel and geometry checks

After every action, the Bun observer repeatedly queried Sway and captured a 1 × 1 grim PPM pixel three pixels inside the page's top-left white margin. A visible result required both a white pixel (all RGB channels >240) and exactly one Überzug++ surface at the expected position and fit-page size, within one pixel. A hidden result required a nonwhite pixel and no Überzug++ surface. This geometry condition prevents counting an old full-width white page as a successful split redraw.

For the successful run, expected image origin was terminal x+13+(pane_left+1)×9 and terminal y+24+(pane_top+2)×21. Fit dimensions came from the 1035:1340 aspect ratio and the requested cell limits. The observer stopped at the first matching sample, or after two seconds. Its loops sampled actual display state; they did not insert settling sleeps. Consequently these results show eventual sampled placement, not an assertion that every intervening compositor frame was correct.

## Twenty complete cycles

Every row completed all nine actions. H/V indicate the split option. All values are milliseconds from immediately before issuing the action to the first matching pixel-plus-geometry sample. No row in this run was affected by the later workspace interruption.

| Cycle | Split | Split ms | Other pane | Back | Next window | Previous window | Zoom | Unzoom | Resize | Kill other | Mismatches |
| ---: | :---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | H | 98.3 | 42.0 | 74.8 | 41.9 | 83.0 | 141.3 | 116.8 | 132.8 | 125.2 | 0 |
| 2 | V | 124.8 | 41.7 | 91.3 | 33.3 | 100.0 | 125.2 | 141.1 | 117.1 | 124.9 | 0 |
| 3 | H | 125.7 | 41.1 | 74.6 | 41.6 | 90.8 | 125.0 | 125.0 | 133.1 | 141.9 | 0 |
| 4 | V | 124.6 | 42.1 | 74.9 | 41.0 | 83.8 | 148.7 | 125.6 | 116.6 | 133.5 | 0 |
| 5 | H | 133.9 | 40.2 | 75.3 | 36.9 | 88.5 | 123.8 | 124.8 | 124.9 | 124.6 | 0 |
| 6 | V | 125.6 | 40.9 | 99.2 | 42.9 | 73.9 | 141.8 | 107.4 | 124.9 | 142.0 | 0 |
| 7 | H | 124.5 | 33.6 | 91.8 | 41.1 | 74.5 | 133.6 | 125.3 | 124.7 | 124.6 | 0 |
| 8 | V | 124.5 | 38.8 | 85.6 | 42.0 | 83.1 | 141.5 | 108.1 | 141.9 | 132.6 | 0 |
| 9 | H | 125.4 | 42.1 | 99.2 | 41.8 | 83.5 | 124.4 | 132.8 | 124.4 | 133.4 | 0 |
| 10 | V | 134.0 | 41.1 | 82.6 | 39.2 | 102.6 | 134.0 | 148.1 | 116.5 | 132.7 | 0 |
| 11 | H | 133.8 | 41.3 | 74.6 | 42.8 | 98.8 | 124.3 | 133.2 | 124.4 | 134.4 | 0 |
| 12 | V | 132.3 | 41.5 | 84.2 | 41.7 | 90.0 | 124.5 | 124.7 | 132.9 | 125.3 | 0 |
| 13 | H | 124.3 | 38.9 | 77.8 | 41.9 | 90.6 | 141.7 | 116.9 | 132.6 | 126.0 | 0 |
| 14 | V | 124.3 | 40.8 | 99.9 | 41.9 | 82.3 | 124.6 | 133.4 | 124.5 | 124.9 | 0 |
| 15 | H | 118.1 | 41.0 | 99.6 | 41.5 | 99.6 | 125.7 | 132.9 | 140.5 | 134.4 | 0 |
| 16 | V | 148.4 | 41.8 | 184.4 | 40.1 | 107.3 | 125.5 | 132.9 | 141.2 | 141.4 | 0 |
| 17 | H | 133.2 | 41.1 | 108.4 | 41.5 | 91.4 | 117.6 | 148.7 | 124.9 | 150.7 | 0 |
| 18 | V | 124.5 | 47.3 | 75.5 | 40.9 | 92.6 | 123.6 | 132.1 | 117.1 | 124.0 | 0 |
| 19 | H | 133.4 | 41.8 | 91.0 | 41.3 | 74.8 | 141.2 | 116.9 | 133.2 | 125.4 | 0 |
| 20 | V | 132.5 | 40.5 | 92.1 | 40.9 | 74.5 | 124.4 | 125.5 | 124.7 | 142.0 | 0 |

### Latency and per-event cost

| Operation | Samples | Median ms | Range ms |
| --- | ---: | ---: | ---: |
| Select other pane; page disappears | 20 | 41.1 | 33.6–47.3 |
| Select reader pane; page appears | 20 | 88.3 | 74.6–184.4 |
| Next tmux window; page disappears | 20 | 41.5 | 33.3–42.9 |
| Previous tmux window; page appears | 20 | 89.3 | 73.9–107.3 |
| Horizontal split, correct fitted page | 10 | 125.6 | 98.3–133.9 |
| Vertical split, correct fitted page | 10 | 125.2 | 124.3–148.4 |
| Zoom | 20 | 125.4 | 117.6–148.7 |
| Unzoom | 20 | 125.6 | 107.4–148.7 |
| Terminal resize | 20 | 124.9 | 116.5–141.9 |
| Kill other pane | 20 | 132.7 | 124.0–150.7 |

The event log through this run contained 468 events, including initial setup and the preliminary split trial. There were no socket errors. It recorded 41 focus-in and 41 focus-out input records; the preliminary isolated pane switch also produced literal `ESC [ O` and `ESC [ I`. Therefore **tmux 3.8 did deliver pane/window focus reports to the pane process with focus-events enabled** in this configuration. This does not establish reliable Sway workspace-focus propagation.

| Reader-loop wall time | Samples | Median ms | Range ms |
| --- | ---: | ---: | ---: |
| tmux query subprocess | 468 | 2.336 | 1.612–7.572 |
| Query, decision, and optional socket write | 468 | 2.380 | 1.661–7.581 |
| Iterations that sent image commands | 228 | 2.502 | 1.661–4.820 |
| Unchanged-state iterations | 240 | 2.278 | 1.699–7.581 |

These are measured elapsed costs, **not CPU time**, and exclude Überzug++ image decoding/presentation after the socket write. Visible latencies include Sway queries, tmux query processes, grim launch/capture, and compositor timing. They do not measure complete-page acknowledgement or sub-frame latency. The revised Sway-aware loop adds further query work; its final per-event distribution was not measured.

## Later failure, one attempted fix, and interruption

After the 20-cycle run, the dedicated workspace was no longer visible, and the focused desktop window was the owner's Alacritty elsewhere. Nevertheless the private tmux query reported `attached,focused,UTF-8` and its pane/window remained active. Follow-up pane/window events could still send add commands. Sway then showed a page surface at x=1142, y=863, width=875, height=1134 on DP-1, while the hidden probe terminal remained at x=3680, y=797, width=1000, height=1273 on HDMI-A-1. **Measured failure:** the image surface was outside the reader's visible terminal and remained on another workspace/output.

[INFERENCE] Terminal focus reports plus tmux state are insufficient to describe Sway workspace visibility, and Überzug++ surface creation/positioning depends on desktop focus/output state. This is a separate failure class from the original delayed stock-hook removal.

The one attempted change subscribed to Sway `window` and `workspace` notifications, queued them into the same loop, and queried the matching Alacritty node's `visible && focused` values before adding an image. Its state key included that desktop visibility result. Setup logs showed false/true transitions and removals/redraw decisions, but there was no completed 20-cycle rerun. Two new Alacritty windows exited with status 0 after approximately 26.4 seconds and 2.9 seconds, leaving the private tmux reader detached. An earlier probe terminal also exited normally after about 257.6 seconds. Logs said “Goodbye”; no Go crash was established. The owner was using the desktop, so these exits/focus changes are treated as **environment interruption**, not evidence that the new code itself crashed.

Post-run follow-up pixel expectations were invalid once the workspace was hidden; they are not added as failures to the completed 20-cycle table. The independently observed image on the wrong visible output remains a real lifecycle failure. The Sway-aware change is **unproven**, not a second passing implementation.

## Exit and SIGKILL

The prototype used a separate liveness Unix connection from reader to outside-tmux host. The host was written to close the image child's stdin and send SIGTERM when that connection ended. A reader SIGKILL should close this connection at the kernel level; this is **an implementation expectation, not a measured SIGKILL result**. A single loop in a killed reader cannot itself perform cleanup. An external process or an image child with a demonstrated parent-death mechanism is required.

The requested deliberate reader-exit and SIGKILL pixel timings were not reached before the owner stopped the experiment. Normal Alacritty exits removed the launched host/image processes, but they are not substituted for the missing reader-SIGKILL test. The first probe's measured image-process SIGTERM result remains separate evidence.

## Alternatives and native protocol checks

### Own Alacritty or Sway-side reader

Measured feasibility: the outside-tmux launcher successfully displayed sharp pages in its own Alacritty and avoided stock tmux hooks. It is a workable experiment arrangement, but does not eliminate separate Wayland surfaces, Sway visibility handling, or the extra launcher requirement. Running the reader without tmux in its own Alacritty would remove pane-placement complexity; its desktop lifecycle would still need proving. This is not evidence for a safe cutover in the owner's existing tmux pane.

The first probe already measured Zathura's useful sharp rendering and faster sampled page changes. A Sway/Zathura wrapper remains a practical alternative when native terminal images are unavailable, though it gives up the original inside-terminal requirement. Symbol-only chafa remains a preview rather than equivalent readable PDF text.

### Alacritty sixel / kitty / iTerm2

A temporary direct-terminal harness ran `chafa --probe off --format sixels|kitty|iterm --size 20x10` on a solid 160 × 160 magenta PNG, cleared the terminal to black first, and sampled a 300 × 300 region with grim for roughly 400 ms per format. All conversions exited 0.

| Terminal / format | Output bytes | Maximum exact-magenta pixels sampled | Captures | Sampling interval ms |
| --- | ---: | ---: | ---: | ---: |
| Alacritty / sixels | 306 | 0 | 31 | 413.0 |
| Alacritty / kitty | 183906 | 0 | 29 | 405.9 |
| Alacritty / iterm | 181723 | 0 | 29 | 409.5 |
| foot / sixels | 237 | 0 | 24 | 405.7 |
| kitty / kitty | 179577 | 0 | 29 | 407.2 |

**Interpretation limitation:** the supposedly image-capable comparison terminals also yielded zero with this harness. Therefore the Alacritty samples are consistent with absent support but are **not a validated proof of incompatibility**; the comparison did not establish a positive pixel reference. An initial run used startup rather than live terminal geometry; the table uses a second pass querying live geometry, and the positive-reference problem remained. The owner stopped further experiments, so this measurement limitation was not investigated further. Do not interpret the zeroes as evidence that foot or kitty lacks its image protocol.

### Packages installed during the comparison

Neither foot nor kitty was initially installed. Repository queries found foot 1.28.0-2 and kitty 0.49.2-1 available. A passwordless `sudo -n pacman -S --needed --noconfirm foot kitty` transaction was already running when the lead's subsequent “do not install” message arrived; it completed successfully in 4.70 seconds. Installed packages were foot, kitty, kitty-shell-integration, kitty-terminfo, and librsync, with pacman's reported total installed size 77.68 MiB. No further installation was started, and packages were not removed without instruction.

### Recommendation

The owner selected **Ghostty with native kitty graphics** during this probe. That supersedes overlay implementation. Test the chosen terminal's native protocol with a positive pixel reference and then implement a terminal-owned display path; do not treat either this overlay's 20-cycle subset or the unsuccessful protocol comparison harness as that proof. No Ghostty protocol measurement was performed here.

## Cleanup and checks actually run

- Built the temporary Go prototype with `go build`; formatted it once with `gofmt` to make the one attempted change readable.
- Smoke-ran the real Poppler/Überzug++/Alacritty/tmux combination and recorded the 20 complete cycles above.
- Ran the direct-terminal chafa conversions and grim sampling above.
- No repository Go package was changed; no repository tests or vet run is reported.
- Stopped the private `probe2` tmux server. Process-name checks found no remaining Überzug++ process or scratch-path reader/protocol process at cleanup. Removed the scratch directory afterwards. No probe helper was retained in the repository.

## Source references

- [Überzug++ v2.9.10 tmux implementation](https://github.com/jstkdng/ueberzugpp/blob/v2.9.10/src/tmux.cpp): environment test, offsets, and hook registration.
- [Überzug++ v2.9.10 Sway implementation](https://github.com/jstkdng/ueberzugpp/blob/v2.9.10/src/canvas/wayland/config/sway.cpp): terminal matching and output-relative window placement.
- [Überzug++ v2.9.10 application](https://github.com/jstkdng/ueberzugpp/blob/v2.9.10/src/application.cpp): command processing and separate socket loop.
- [Überzug++ v2.9.10 dimensions](https://github.com/jstkdng/ueberzugpp/blob/v2.9.10/src/dimensions.cpp): tmux offset application.
- [First probe](DISPLAY_PROBE.md): stock-hook failure, page appearance, and Zathura comparison.
