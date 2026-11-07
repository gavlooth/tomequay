# Terminal PDF Reader

A keyboard-first PDF reader with independent document tabs and real terminal page images. The supported display path is **Ghostty inside tmux on Sway**, using Kitty Unicode placeholders (not overlays or direct graphics placements).

Requires Go and Poppler (`pacman -S poppler` on Arch, or `apt install poppler-utils` on Debian/Ubuntu). For PDF outlines, also install qpdf (`pacman -S qpdf` or `apt install qpdf`). In tmux, enable graphics passthrough with `tmux set -g allow-passthrough on`.

```sh
go build ./cmd/pdfreader
./pdfreader [--cache-mb 128] [--page 1] [file.pdf ...]
```

No files opens the in-app picker. Use `j/k` to scroll, `J/K` for pages, `12G` to jump, `+/-` to zoom, `zw/zp` to fit width/page, `/query` then `n/N` to search, `gd` to duplicate, and `gt/gT` to switch tabs. `gO` or `:outline` opens the document outline: `j/k` selects, `Enter` jumps, `/` filters titles (then `Enter` finishes editing the filter), and `Esc` closes. Entries without a resolvable page are dimmed and skipped; the current page's section is selected initially. Outline jumps join the normal jump history. `Ctrl-l` forces a page re-upload/redraw. `o` opens the picker, `q` closes a tab, and `Q` quits. `F1` or `:help` shows all keys; the [key table](docs/PLAN.md#key-bindings) also documents commands, marks, and jump history.

The last 50 opened paths are saved in `$XDG_STATE_HOME/pdfreader/recent` (default `~/.local/state/pdfreader/recent`); no reading state or passwords are saved. Outlines load asynchronously through qpdf and are shared between tabs for the same canonical file path and modification time; reloading drops them. Missing qpdf and PDF errors appear in the status line without preventing normal reading. See the [display proof](docs/DISPLAY_PROBE_KITTY.md) for the supported image protocol and its measured limits.
