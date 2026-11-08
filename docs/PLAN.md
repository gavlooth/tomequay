# Development plan

## Goal and environment

Build a keyboard-first PDF reader that stays inside an Alacritty tmux pane on Sway. It must open several PDFs and allow two tabs for the same PDF, with independent page, zoom, and scroll positions. PDF files are read-only.

Observed on this machine: Alacritty 0.17.0, tmux 3.8, Sway, Go, `pdftoppm`, `pdftotext`, `pdfinfo`, `chafa`, and Zathura are installed. `ueberzugpp` and `mutool` are not installed. These are observations, not project requirements for every machine.

## Choice of approach

| Approach | Expected benefit | Runtime cost | Build and maintenance effort | Decision |
| --- | --- | --- | --- | --- |
| Zathura windows in a Sway tabbed layout | Full-quality pages and existing PDF features | Low; separate GUI windows | Very low | Useful comparison, but fails the in-terminal requirement |
| Fork `pdfterm` and replace Kitty output | Reuses its navigation, search, and tabs | Potentially low after replacement | High: its display and event handling are tied to Kitty graphics and Rust | Do not start here |
| New Go TUI with Poppler commands and Überzug++ overlay | Full-quality pages in Alacritty/tmux on supported local displays; tabs can hold independent views | One renderer process for a cache miss; cached pages avoid repeat work | Moderate: tab model is simple; overlay positioning and cleanup need care | Chosen, subject to the display probe |
| New Go TUI with embedded MuPDF | Direct PDF APIs for richer interactions | Avoids a process per rendered page | Higher: native integration, packaging, and API work | Consider only if Poppler process cost or PDF features become measured limits |

Zathura is a GTK application, not a rendering library for a TUI. Poppler is already installed here and can supply page images and extracted text. Überzug++ draws images in a window above terminal cells; its documented tmux support includes Sway. Without that overlay, Alacritty can show text or block-character previews, but ordinary PDF text in a page image will often be too small to read.

## Display probe before application code

**Hypothesis:** A page rendered by `pdftoppm` and shown by Überzug++ can remain crisp and correctly placed inside an Alacritty tmux pane on Sway while the user switches panes, switches tabs, and resizes the terminal.

1. Install Überzug++ locally, render one text-heavy page and one diagram-heavy page with `pdftoppm`, and display them in a tmux pane. Use an existing PDF of at least 100 pages for repeated page changes.
2. Record page-render time, image-update time, peak memory, placement errors, and cleanup after pane changes and process exit. Compare the display with Zathura and with `chafa` in the same terminal size.
3. Stop the overlay design if it cannot reliably place and remove images in this exact Sway/Alacritty/tmux setup. Do not build the reader around an unproven overlay. Set later speed targets from these measurements and the current Zathura experience, allowing for the value of staying in tmux.

The predicted useful signal is readable text and diagrams at the pane's pixel resolution, with no images left over after tab or pane changes. A successful single page without pane switching is insufficient.

## Application design after a successful probe

```text
keyboard / terminal resize
        ↓
Go TUI: tabs, file picker, page navigation, status line
        ↓
per-tab view state ── shared document metadata and page cache
        ↓
Poppler commands ── page PNGs and extracted text
        ↓
Überzug++ ── image placed within the active tmux pane
```

- Keep a separate tab ID and view state for every opening, even when paths match. Share page count, file change time, and rendered pages by canonical path and render size.
- Render the visible page and prefetch its neighbors. Cancel obsolete work when a tab changes. Limit the global image cache by bytes, not by number of pages.
- Start Poppler commands with argument arrays, never a shell command string. Handle missing tools, invalid PDFs, password prompts, and renderer failures in the status line.
- Tie image placement to the active pane's cell coordinates and size. Remove the previous image before replacing it, on tab close, on resize, and on process exit.
- Keep image output local to the desktop session. Remote SSH reading would need a separate design for transferring pages to the local display.

The main event flow is:

```text
open(path): add a tab with its own view; reuse document data if path is already open
change_view(tab): cancel obsolete render; use cached page or render the requested size
page_ready(tab, page): show it only if that tab and page are still active
switch_tab / resize / exit: remove the old image; then place the current image if needed
```

At 1200 × 1600 pixels, an uncompressed RGBA page is about 7.3 MiB. Three pages for each of ten unrelated tabs would use about 220 MiB before other overhead; this is an estimate, not a measurement. A shared cache with a roughly 128 MiB starting limit should be measured against the actual PNG sizes and page-change behavior before the limit is fixed.

## Key bindings

Vim-style normal mode with a `:` command line, a `/` search prompt, and an extracted-text view. A number typed first repeats or targets (`12G`, `3J`, `5j`). `Esc` clears a pending count, prefix, or prompt. All bindings are configurable in the polish step; these are the defaults.

| Keys | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | Scroll down / up one step; at the page edge, continue onto the next / previous page |
| `h` / `l`, `←` / `→` | Scroll left / right (when zoomed beyond the pane) |
| `Ctrl-d` / `Ctrl-u` | Scroll half a screen |
| `Ctrl-f` / `Space`, `Ctrl-b` / `b` | Scroll a full screen, flipping page at the edge |
| `J` / `K`, `PageDown` / `PageUp` | Next / previous page, top of page |
| `gg`, `G`, `{N}G` | First page, last page, page N |
| `H` / `M` / `L` | Scroll to top / middle / bottom of the page |
| `+` / `-`, `zi` / `zo` | Zoom in / out |
| `zw`, `zp` (`zz`), `z0` | Fit width, fit page, reset zoom to fit |
| `]b` / `[b`, `gt` / `gT`, `{N}gt` | Next / previous tab (`]b`, `[b` and `:bn`, `:bp` need no Shift), tab N |
| `gd` (`:tab split`) | Duplicate the current tab; the copy navigates independently |
| `gb`, `:ls`, `:tabs`, `:buffers`, `:b` | List open tabs and pick one |
| `gl` | Find an open tab with fzf, with page-text preview; `Enter` switches |
| `gp`, `Ctrl-^` | Return to the last active tab |
| `:b N`, `:b name` | Select tab N or a tab whose name matches |
| `:e %`, `:tabnew %` | Open the current file again as an independent tab |
| `g<` / `g>`, `:tabmove N` / `+N` / `-N` | Move the current tab left / right, or to position N |
| `:tabonly` | Close all other tabs |
| `zd`, `:dark` | Toggle dark page colours |
| `gs`, `:sidebar` | Toggle the outline sidebar |
| `o`, `:e {path}` | Open a PDF in a new tab (file picker / path) |
| `Ctrl-p` | Find PDFs with fzf from history and scan roots; `Tab` selects several, `Enter` opens independent tabs, `Esc` cancels |
| `gh`, `:history` | Reading history: `j/k` or arrows select, `Enter` opens a new tab, `d` forgets the entry without deleting its file, `/` filters, `Esc` closes; missing files are dimmed |
| `q`, `:q` | Close the current tab; quit when it is the last |
| `Q`, `:qa` | Quit all tabs |
| `/`, `?`, `n` / `N` | Search forward / backward, next / previous match |
| `g/`, `:grep QUERY` | Find matching text lines with fzf and highlighted page preview; first 200 matching pages, `Enter` jumps and keeps the query for `n/N` |
| `v`, `:text` | Open the current page's extracted text for selection and copying |
| Prompt `↑` / `↓`, `Ctrl-p` / `Ctrl-n` | Walk command/search history matching the typed prefix; past newest restores the typed text |
| Prompt `Ctrl-u`, `Ctrl-w` | Clear the prompt line / delete the preceding word |
| `m{a-z}`, `'{a-z}`, `''` | Set mark, jump to mark, jump back to the previous position |
| `Ctrl-o` / `Ctrl-i` | Older / newer position in the jump list |
| `r` | Reload the file |
| `gO` (`:outline`) | Outline navigation through qpdf, if the PDF has one; `j/k` selects, `Enter` jumps, `/` filters titles, `Esc` closes |
| `go` | Find an outline section with fzf, preserving indentation; preview the page, `Enter` jumps and records jump history |
| `:{N}`, `:tabnew`, `:tabclose` | Go to page N, new tab, close tab |
| `F1`, `:help` | Key help |

Keys are decoded into named actions in one table, so the status line, `:help`, and the tests read the same table.

### Text view

| Keys / mouse | Action |
| --- | --- |
| `h/j/k/l`, arrows | Move the text cursor left / down / up / right |
| `Home` / `End` | Move to the start / end of the line |
| `Ctrl-u` / `Ctrl-d` | Move half a screen up / down |
| `v` | Toggle the selection anchor; cursor movement extends the selection |
| Left-button drag | Select text |
| Mouse wheel | Scroll the text |
| `a` | Select the whole page |
| `y` | Send the selection to the clipboard using OSC52 |
| `Esc` | Close the text view without changing the PDF position |

Copying needs terminal OSC52 support and clipboard-write permission. In tmux, enable `tmux set -g allow-passthrough on`; mouse interaction also needs `tmux set -g mouse on`. Sending is not acknowledged: the terminal may reject the clipboard update. Image-only pages need OCR, which is not included.

## Implementation order

1. **Display proof:** verify Poppler rendering and Überzug++ placement, cleanup, pane switching, and resize behavior. Record results before writing the full reader.
2. **Core reader:** Go TUI with a tab bar, file opening, tab close, duplicate-current-tab, next/previous tab, page navigation, fit-page/fit-width, zoom, and independent view state. A duplicate starts at the current view and then navigates independently.
3. **Reading tools:** text search with page results, outline navigation through the optional qpdf CLI, recent files, and live reload when a PDF changes.
4. **Polish:** mouse wheel, clear errors, configurable keys, cache tuning, packaging, and documentation. Verify with long text PDFs, scanned PDFs, diagrams, duplicate tabs, and 10 open documents.

For the first version, annotations, PDF editing, and cross-machine image transport are outside the scope.

## Completion checks

- Opening one PDF twice creates two visible tabs with independent page, zoom, and scroll state.
- Switching tabs or tmux panes does not leave an image over another view.
- Resizing keeps the page within the pane; closing the reader removes its image.
- Search and page navigation work on a long text PDF; scanned PDFs remain viewable even when text search finds nothing.
- On a 100-page PDF and with 10 tabs open, report measured page-change latency and peak memory. Compare them with the display probe and Zathura; any performance gate must follow the measured baseline and the benefit of the in-terminal workflow.

## References

- [Alacritty image support in ratatui-image's compatibility table](https://github.com/ratatui/ratatui-image#compatibility-matrix)
- [Überzug++ README: Wayland, tmux, and image placement](https://github.com/jstkdng/ueberzugpp)
- [Yazi image preview documentation: Alacritty and tmux](https://yazi-rs.github.io/docs/image-preview/)
- [Poppler `pdftoppm` manual](https://manpages.debian.org/stable/poppler-utils/pdftoppm.1.en.html)
- [Zathura project and PDF backends](https://pwmt.org/projects/zathura/)
