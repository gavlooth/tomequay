// Package keys decodes the reader's vim-style keys without reading a terminal
// or depending on a TUI library. The caller feeds decoded Key values and applies
// completed Commands. Decoder is intended for one event-loop goroutine.
//
// Counts start with 1 through 9 and may contain later zeroes. Count zero means
// no count was typed. G and gt select a numbered page or tab when counted; other
// commands retain the count for the caller to interpret. An overflowing count
// discards its complete sequence. Esc clears counts, prefixes, and prompts.
// Unknown prefix sequences consume the prefix and the unexpected key; the next
// key starts fresh. Prefixes g, z, m, and ' never need a timeout. Marks accept
// lowercase a through z; two apostrophes jump back instead of selecting a mark.
//
// The :, /, and ? keys enter prompts and emit nothing until Enter. Search uses
// Search mode for either direction, with / or ? retained in Pending. An empty
// search emits its direction and empty Text so the caller can reuse its last
// query. Backspace removes one rune; Backspace on an already empty prompt returns
// to Normal. An invalid command-line submission stays open for editing, and its
// diagnostic is available by passing Pending to ParseCommandLine. A successful
// submission or Esc returns to Normal. Empty command lines close without action.
//
// ParseCommandLine treats :e's remaining path literally after skipping separator
// whitespace. Internal and trailing spaces survive. Do not add shell quotes or
// escapes: they become filename characters. No shell or filename expansion is
// performed. Prefix a path whose first character is whitespace with ./.
// The exact path % in :e % or :tabnew % emits DuplicateTab with Text "%"; the
// caller reopens the current file. :tab split emits DuplicateTab with empty Text.
//
// TabList is reached with gb, :ls, :tabs, :buffers, or bare :b. With an argument,
// :b N selects a one-based GoToTab Count and :b name emits GoToTabByName with the
// trimmed name fragment in Text. PreviousUsedTab (gp or Ctrl-^) returns to the
// last active tab. The terminal supplies Ctrl-^ as Key{Rune: '^', Ctrl: true}.
//
// MoveTab uses Count as an absolute zero-based position when Arg is zero,
// including :tabmove 0. Arg '+' or '-' instead marks a relative displacement,
// with Count carrying the signed value. Thus :tabmove -2 yields Count -2 and
// Arg '-', while :tabmove +2 yields Count 2 and Arg '+'. g< and g> use the same
// relative form with Count -1 and 1, or the signed typed count. Explicit +0 and
// -0 stay relative no-ops. CloseOtherTabs is :tabonly. ToggleDark is zd/:dark,
// and ToggleSidebar is gs/:sidebar. The caller performs all tab and view changes.
//
// Help is generated from the same normal-mode and command-line tables used for
// decoding. HelpAction is the action for F1 and :help; its name avoids a Go name
// collision with the Help function. Question mark starts backward search, not
// help. The q key closes a tab rather than recording a vim macro. Tab is treated
// as Ctrl-i, matching terminals that cannot distinguish those inputs.
// Home and End select the first and last page, respectively. Ctrl-l requests
// re-uploading and redrawing the visible page without reloading its PDF file.
package keys
