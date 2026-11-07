# Terminal PDF Reader

A keyboard-first PDF reader with independent document tabs and real terminal page images. The supported display path is **Ghostty inside tmux on Sway**, using Kitty Unicode placeholders (not overlays or direct graphics placements).

Requires Go and Poppler (`pacman -S poppler` on Arch, or `apt install poppler-utils` on Debian/Ubuntu). For PDF outlines, also install qpdf (`pacman -S qpdf` or `apt install qpdf`). In tmux, enable graphics passthrough with `tmux set -g allow-passthrough on`.

```sh
go build -trimpath -ldflags="-s -w" -o ~/.local/bin/pdfr ./cmd/pdfr
pdfr [--cache-mb 128] [--page 1] [file.pdf ...]
```

No files opens the in-app picker. Use `j/k` to scroll, `J/K` for pages, `12G` to jump, `+/-` to zoom, `zw/zp` to fit width/page, `/query` then `n/N` to search, `gd` to duplicate, and `]b`/`[b` (or `gt`/`gT`) to switch tabs. `gO` or `:outline` opens the document outline: `j/k` selects, `Enter` jumps, `/` filters titles (then `Enter` finishes editing the filter), and `Esc` closes. Entries without a resolvable page are dimmed and skipped; the current page's section is selected initially. Outline jumps join the normal jump history. `Ctrl-l` forces a page re-upload/redraw. `o` opens the picker, `q` closes a tab, and `Q` quits. `F1` or `:help` shows all keys; the [key table](docs/PLAN.md#key-bindings) also documents commands, marks, and jump history.

Chrome uses the dark, 24-bit Ghostty palette with highlighted tabs, a reading-progress status bar, and bordered help, outline, and picker panels. Nerd Font icons and Powerline separators are enabled by default; run `PDFR_ASCII=1 pdfr file.pdf` for ASCII decorations without a patched font. Long file names retain their suffix, and duplicate tabs show their independent page numbers.

The last 50 opened paths are saved in `$XDG_STATE_HOME/pdfr/recent` (default `~/.local/state/pdfr/recent`); no reading state or passwords are saved. Outlines load asynchronously through qpdf and are shared between tabs for the same canonical file path and modification time; reloading drops them. Missing qpdf and PDF errors appear in the status line without preventing normal reading. See the [display proof](docs/DISPLAY_PROBE_KITTY.md) for the supported image protocol and its measured limits.

## Tabs and the switcher

Every opening creates an independent tab, even for an already-open file; document metadata and rendered pages are shared. `gd`, `:e %`, and `:tabnew %` copy the current view into another tab. `]b`/`[b` or `gt`/`gT` cycle tabs (`:bn`/`:bp` too), `{N}gt` or `:b N` selects a numbered tab, and `gp` or `Ctrl-^` toggles between the two most recently used tabs. `:b name` matches a case-insensitive file-name fragment; ambiguous matches open the filtered switcher.

`gb`, `:ls`, `:tabs`, `:buffers`, or bare `:b` opens the switcher in tab order. It shows each tab's page, fit/zoom and dark setting, directory hints for colliding names, `*` for the active tab and `#` for the previous tab. Use `j/k`, `Enter`, or digits `1–9` to switch; `x` closes the highlighted tab, `d` duplicates it, `/` filters names (`Enter` finishes editing), and `Esc` closes. `g</g>` move a tab left/right; `:tabmove N` uses a zero-based position, and `:tabmove +N/-N` moves relatively. `:tabonly` closes all other tabs.

## Mouse

Enable tmux mouse forwarding with `tmux set -g mouse on`. Wheel up/down scrolls like `k/j`; Ctrl+wheel zooms, Shift+wheel scrolls horizontally. Click a tab to focus it or middle-click to close it. A single click jumps to a switcher, outline, or sidebar row; the file picker selects on one click and opens on a double-click or `Enter`. Drag the page to pan a zoomed view. Without tmux mouse forwarding, all keyboard commands still work. Shift+drag selects text in tmux/Ghostty instead of panning the PDF.

## Dark pages

`zd` or `:dark` toggles light text on near-black paper for the current tab only. Duplicating copies the setting, after which both tabs are independent; reload preserves it. Light and dark images have separate shared cache entries, so switching back reuses a cached rendering.

## Outline sidebar

`gs` or `:sidebar` toggles an outline-only left panel. It follows the current page's section, and clicking a section jumps there. The sidebar uses at most 30 columns or one third of the pane and hides automatically below 72 columns. Pages reflow when its available width changes; uploaded images remain cached. The sidebar is display + mouse, not a separate keyboard mode: use `gO` for the keyboard outline overlay. Documents without an outline show a dim `no outline` line.
