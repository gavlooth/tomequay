# Tomequay

A keyboard-first PDF reader with independent document tabs and real terminal page images. The supported display path is **Ghostty inside tmux on Sway**, using Kitty Unicode placeholders (not overlays or direct graphics placements).

Requires Go and Poppler (`pacman -S poppler` on Arch, or `apt install poppler-utils` on Debian/Ubuntu). For PDF outlines, also install qpdf (`pacman -S qpdf` or `apt install qpdf`). In tmux, enable graphics passthrough with `tmux set -g allow-passthrough on`.

```sh
go build -trimpath -ldflags="-s -w" -o ~/.local/bin/pdfr ./cmd/pdfr
pdfr [--cache-mb 128] [--page 1] [file.pdf ...]
```

No files opens the in-app picker. Use `j/k` to scroll, `J/K` for pages, `12G` to jump, `+/-` to zoom, `zw/zp` to fit width/page, `/query` then `n/N` to search, `gd` to duplicate, and `]b`/`[b` (or `gt`/`gT`) to switch tabs. `gO` or `:outline` opens the document outline: `j/k` selects, `Enter` jumps, `/` filters titles (then `Enter` finishes editing the filter), and `Esc` closes. Entries without a resolvable page are dimmed and skipped; the current page's section is selected initially. Outline jumps join the normal jump history. `Ctrl-l` forces a page re-upload/redraw. `o` opens the picker, `q` closes a tab, and `Q` quits. `F1` or `:help` shows all keys; the [key table](docs/PLAN.md#key-bindings) also documents commands, marks, and jump history.

Chrome uses the dark, 24-bit Ghostty palette with highlighted tabs, a reading-progress status bar, and bordered help, outline, and picker panels. Nerd Font icons and Powerline separators are enabled by default; run `PDFR_ASCII=1 pdfr file.pdf` for ASCII decorations without a patched font. Long file names retain their suffix, and duplicate tabs show their independent page numbers.

Reading positions for the last 500 canonical file paths are saved in `$XDG_STATE_HOME/pdfr/history.json` (default `~/.local/state/pdfr/history.json`): page, fit, zoom, scroll, dark setting, and last-opened time. Openings restore the saved view unless `--page` is supplied; duplicates copy the live view. Changes are saved at most every five seconds while reading, and on tab close and quit. Multiple readers safely merge updates under a file lock. The old `recent` list is migrated once; passwords are never saved.

`gh` or `:history` opens reading history, newest first, with file name, directory, last page/total, and relative time. Use `j/k` or arrows, `Enter` to open a new tab, `d` to forget an entry (not delete its file), `/` to filter, and `Esc` to close. Missing files are dimmed and cannot open. The file picker's `recent:` rows use this same history.

In `:`, `/`, and `?` prompts, `Up/Down` or `Ctrl-p/Ctrl-n` walk previous entries matching the text already typed; walking down past the newest restores that text. Command and search histories are separate and retain 200 entries across restarts. `Ctrl-u` clears the line and `Ctrl-w` erases the preceding word. The last search query is remembered; `n/N` re-run it after a restart.

Outlines load asynchronously through qpdf and are shared between tabs for the same canonical file path and modification time; reloading drops them. Missing qpdf and PDF errors appear in the status line without preventing normal reading. See the [display proof](docs/DISPLAY_PROBE_KITTY.md) for the supported image protocol and its measured limits.

## Tabs and the switcher

Every opening creates an independent tab, even for an already-open file; document metadata and rendered pages are shared. `gd`, `:e %`, and `:tabnew %` copy the current view into another tab. `]b`/`[b` or `gt`/`gT` cycle tabs (`:bn`/`:bp` too), `{N}gt` or `:b N` selects a numbered tab, and `gp` or `Ctrl-^` toggles between the two most recently used tabs. `:b name` matches a case-insensitive file-name fragment; ambiguous matches open the filtered switcher.

`gb`, `:ls`, `:tabs`, `:buffers`, or bare `:b` opens the switcher in tab order. It shows each tab's page, fit/zoom and dark setting, directory hints for colliding names, `*` for the active tab and `#` for the previous tab. Use `j/k`, `Enter`, or digits `1–9` to switch; `x` closes the highlighted tab, `d` duplicates it, `/` filters names (`Enter` finishes editing), and `Esc` closes. `g</g>` move a tab left/right; `:tabmove N` uses a zero-based position, and `:tabmove +N/-N` moves relatively. `:tabonly` closes all other tabs.

## Optional fzf pickers

Install `fzf` for four additional fuzzy views: `Ctrl-p` finds PDFs, `gl` finds an open tab, `go` finds an outline section, and `g/QUERY` or `:grep QUERY` finds matching text lines. Each view previews the selected PDF page using this same `pdfr` binary and Poppler. `Tab` selects multiple files in the file view; `Enter` opens each in a new independent tab, including files already open. Outline and text selections join the jump list; text selections keep the query active for `n/N`. `Esc` cancels without changing the view. The built-in `o`, `gb`, `gO`, and `/` remain available even without fzf.

File candidates start with reading history, then stream PDFs found by `fd` (or `find`) to depth 3, capped at 5000 files. Set `PDFR_ROOTS=/papers:/books` to replace the default scan roots: the current document's directory (working directory if none), `~/Documents`, `~/Downloads`, and `~`. Text search extracts context from the first 200 matching pages; the header states that limit, while `n/N` can visit all matching pages. Page text is cached in a private temporary directory for the reader session and removed on exit; no text sidecars are written beside PDFs.

Inside tmux, fzf uses a popup while the reader keeps its image cells. The reader finishes outstanding image acknowledgements before opening it and redraws the cells when it closes. Outside tmux, the reader pauses its sole input reader, leaves the alternate screen and restores cooked mode, then resumes raw mode and re-uploads the page after fzf exits.

## Mouse

Enable tmux mouse forwarding with `tmux set -g mouse on`. Wheel up/down scrolls like `k/j`; Ctrl+wheel zooms, Shift+wheel scrolls horizontally. Click a tab to focus it or middle-click to close it. A single click jumps to a switcher, outline, or sidebar row; the file picker and reading history select on one click and open on a double-click or `Enter`. Drag the page to pan a zoomed view. Without tmux mouse forwarding, all keyboard commands still work. Shift+drag selects text in tmux/Ghostty instead of panning the PDF.

## Dark pages

`zd` or `:dark` toggles light text on near-black paper for the current tab only. Duplicating copies the setting, after which both tabs are independent; reload preserves it. Light and dark images have separate shared cache entries, so switching back reuses a cached rendering.

## Outline sidebar

`gs` or `:sidebar` toggles an outline-only left panel. It follows the current page's section, and clicking a section jumps there. The sidebar uses at most 30 columns or one third of the pane and hides automatically below 72 columns. Pages reflow when its available width changes; uploaded images remain cached. The sidebar is display + mouse, not a separate keyboard mode: use `gO` for the keyboard outline overlay. Documents without an outline show a dim `no outline` line.
