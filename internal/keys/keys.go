package keys

import (
	"strconv"
	"strings"
	"unicode"
)

// Special identifies a key without a printable rune. Space uses Rune: ' '.
type Special int

const (
	NoSpecial Special = iota
	Up
	Down
	Left
	Right
	PageUp
	PageDown
	Esc
	Enter
	Backspace
	Tab
	F1
	Home
	End
)

// Key is a decoded terminal key. Set either Rune or Special, not both.
// Ctrl letters use their lowercase rune, for example Key{Rune: 'd', Ctrl: true}.
// Tab and Ctrl-i both move to the newer position in normal mode.
type Key struct {
	Rune    rune
	Special Special
	Ctrl    bool
}

// Action names a reader operation; this package never performs the operation.
type Action int

const (
	NoAction Action = iota
	ScrollDown
	ScrollUp
	ScrollLeft
	ScrollRight
	HalfPageDown
	HalfPageUp
	FullPageDown
	FullPageUp
	NextPage
	PreviousPage
	FirstPage
	LastPage
	GoToPage
	ScrollToTop
	ScrollToMiddle
	ScrollToBottom
	ZoomIn
	ZoomOut
	FitWidth
	FitPage
	ResetZoom
	NextTab
	PreviousTab
	GoToTab
	DuplicateTab
	OpenFile
	CloseTab
	QuitAll
	SearchForward
	SearchBackward
	SearchNext
	SearchPrevious
	SetMark
	JumpToMark
	JumpBack
	JumpOlder
	JumpNewer
	Reload
	Redraw
	Outline
	HelpAction
	NewTab
	TabList
	PreviousUsedTab
	GoToTabByName
	MoveTab
	CloseOtherTabs
	ToggleDark
	ToggleSidebar
	OpenCommandLine
)

// Command is a completed input. Count is zero when no count was typed, except
// MoveTab always provides a destination or displacement. Arg is a mark letter
// or a MoveTab sign: zero means an absolute zero-based position, '+' or '-'
// means a relative signed Count. Text holds a path, search query, or tab name.
type Command struct {
	Action Action
	Count  int
	Arg    rune
	Text   string
}

// Mode identifies the current input prompt.
type Mode int

const (
	Normal Mode = iota
	CommandLine
	Search
)

// HelpEntry describes a key sequence or command-line spelling.
// HelpAction is named differently from Help because Go shares their namespace.
type HelpEntry struct {
	Keys        string
	Action      Action
	Description string
}

type binding struct {
	sequence    [2]Key
	action      Action
	counted     Action
	description string
	prompt      Mode
	mark        bool
	countSign   int
	arg         rune
}

func runes(s string) [2]Key {
	var result [2]Key
	for i, r := range s {
		result[i] = Key{Rune: r}
	}
	return result
}

// All normal-mode spellings and prompt starters live in this table.
var bindings = [...]binding{
	{sequence: runes("j"), action: ScrollDown, description: "Scroll down; continue to the next page at the edge"},
	{sequence: [2]Key{{Special: Down}}, action: ScrollDown, description: "Scroll down; continue to the next page at the edge"},
	{sequence: runes("k"), action: ScrollUp, description: "Scroll up; continue to the previous page at the edge"},
	{sequence: [2]Key{{Special: Up}}, action: ScrollUp, description: "Scroll up; continue to the previous page at the edge"},
	{sequence: runes("h"), action: ScrollLeft, description: "Scroll left"},
	{sequence: [2]Key{{Special: Left}}, action: ScrollLeft, description: "Scroll left"},
	{sequence: runes("l"), action: ScrollRight, description: "Scroll right"},
	{sequence: [2]Key{{Special: Right}}, action: ScrollRight, description: "Scroll right"},
	{sequence: [2]Key{{Rune: 'd', Ctrl: true}}, action: HalfPageDown, description: "Scroll down half a screen"},
	{sequence: [2]Key{{Rune: 'u', Ctrl: true}}, action: HalfPageUp, description: "Scroll up half a screen"},
	{sequence: [2]Key{{Rune: 'f', Ctrl: true}}, action: FullPageDown, description: "Scroll down a full screen"},
	{sequence: runes(" "), action: FullPageDown, description: "Scroll down a full screen"},
	{sequence: [2]Key{{Rune: 'b', Ctrl: true}}, action: FullPageUp, description: "Scroll up a full screen"},
	{sequence: runes("b"), action: FullPageUp, description: "Scroll up a full screen"},
	{sequence: runes("J"), action: NextPage, description: "Next page, at its top"},
	{sequence: [2]Key{{Special: PageDown}}, action: NextPage, description: "Next page, at its top"},
	{sequence: runes("K"), action: PreviousPage, description: "Previous page, at its top"},
	{sequence: [2]Key{{Special: PageUp}}, action: PreviousPage, description: "Previous page, at its top"},
	{sequence: runes("gg"), action: FirstPage, description: "First page"},
	{sequence: [2]Key{{Special: Home}}, action: FirstPage, description: "First page"},
	{sequence: runes("G"), action: LastPage, counted: GoToPage, description: "Last page, or page N with a count"},
	{sequence: [2]Key{{Special: End}}, action: LastPage, description: "Last page"},
	{sequence: runes("H"), action: ScrollToTop, description: "Scroll to the top of the page"},
	{sequence: runes("M"), action: ScrollToMiddle, description: "Scroll to the middle of the page"},
	{sequence: runes("L"), action: ScrollToBottom, description: "Scroll to the bottom of the page"},
	{sequence: runes("+"), action: ZoomIn, description: "Zoom in"},
	{sequence: runes("zi"), action: ZoomIn, description: "Zoom in"},
	{sequence: runes("-"), action: ZoomOut, description: "Zoom out"},
	{sequence: runes("zo"), action: ZoomOut, description: "Zoom out"},
	{sequence: runes("zw"), action: FitWidth, description: "Fit the page width"},
	{sequence: runes("zp"), action: FitPage, description: "Fit the whole page"},
	{sequence: runes("zz"), action: FitPage, description: "Fit the whole page"},
	{sequence: runes("z0"), action: ResetZoom, description: "Reset zoom to fit"},
	{sequence: runes("gt"), action: NextTab, counted: GoToTab, description: "Next tab, or tab N with a count"},
	{sequence: runes("gT"), action: PreviousTab, description: "Previous tab"},
	{sequence: runes("]b"), action: NextTab, description: "Next tab; a count moves further"},
	{sequence: runes("[b"), action: PreviousTab, description: "Previous tab; a count moves further"},
	{sequence: runes("gd"), action: DuplicateTab, description: "Duplicate the current tab with its own view"},
	{sequence: runes("gb"), action: TabList, description: "List open tabs"},
	{sequence: runes("gp"), action: PreviousUsedTab, description: "Return to the last active tab"},
	{sequence: [2]Key{{Rune: '^', Ctrl: true}}, action: PreviousUsedTab, description: "Return to the last active tab"},
	{sequence: runes("g<"), action: MoveTab, countSign: -1, arg: '-', description: "Move the tab left by N positions, or one without a count"},
	{sequence: runes("g>"), action: MoveTab, countSign: 1, arg: '+', description: "Move the tab right by N positions, or one without a count"},
	{sequence: runes("zd"), action: ToggleDark, description: "Toggle dark page colors"},
	{sequence: runes("gs"), action: ToggleSidebar, description: "Toggle the sidebar"},
	{sequence: runes("o"), action: OpenFile, description: "Open a PDF in a new tab using the file picker"},
	{sequence: runes("q"), action: CloseTab, description: "Close the current tab; quit if it is the last"},
	{sequence: runes("Q"), action: QuitAll, description: "Quit all tabs"},
	{sequence: runes("/"), action: SearchForward, prompt: Search, description: "Enter a forward search"},
	{sequence: runes("?"), action: SearchBackward, prompt: Search, description: "Enter a backward search"},
	{sequence: runes("n"), action: SearchNext, description: "Next search match"},
	{sequence: runes("N"), action: SearchPrevious, description: "Previous search match"},
	{sequence: runes("m"), action: SetMark, mark: true, description: "Set a lowercase letter mark"},
	{sequence: runes("'"), action: JumpToMark, mark: true, description: "Jump to a lowercase letter mark"},
	{sequence: runes("''"), action: JumpBack, description: "Jump back to the previous position"},
	{sequence: [2]Key{{Rune: 'o', Ctrl: true}}, action: JumpOlder, description: "Older position in the jump list"},
	{sequence: [2]Key{{Rune: 'i', Ctrl: true}}, action: JumpNewer, description: "Newer position in the jump list"},
	{sequence: [2]Key{{Special: Tab}}, action: JumpNewer, description: "Newer position in the jump list (same as Ctrl-i)"},
	{sequence: runes("r"), action: Reload, description: "Reload the file"},
	{sequence: [2]Key{{Rune: 'l', Ctrl: true}}, action: Redraw, description: "Re-upload and redraw the visible page"},
	{sequence: runes("gO"), action: Outline, description: "Open the document outline"},
	{sequence: [2]Key{{Special: F1}}, action: HelpAction, description: "Show key help"},
	{sequence: runes(":"), action: OpenCommandLine, prompt: CommandLine, description: "Enter a command line"},
}

func (b binding) hasSecondKey() bool { return b.mark || b.sequence[1] != (Key{}) }

func (b binding) matchesSecond(k Key) bool {
	if b.mark {
		return k.Special == NoSpecial && !k.Ctrl && k.Rune >= 'a' && k.Rune <= 'z'
	}
	return b.sequence[1] == k
}

// Decoder translates keys on one event-loop goroutine. Its zero value is ready
// to use. It does not read the terminal or use timers.
type Decoder struct {
	mode         Mode
	prefix       Key
	digits       []byte
	count        int
	countTooBig  bool
	prompt       []rune
	promptStart  rune
	searchAction Action
}

func NewDecoder() *Decoder { return &Decoder{} }

func (d *Decoder) Mode() Mode { return d.mode }

// Pending returns the count/prefix or the prompt including its leading :, /, or ?.
func (d *Decoder) Pending() string {
	if d.mode != Normal {
		return string(d.promptStart) + string(d.prompt)
	}
	if d.prefix == (Key{}) {
		return string(d.digits)
	}
	return string(d.digits) + string(d.prefix.Rune)
}

// Feed emits a command only when input is complete. Prompt starters change Mode
// without emitting a command. An invalid command-line submission remains open
// for editing; ParseCommandLine supplies its error. Empty command lines close.
// An unknown key after a prefix drops that key too, then starts fresh.
// Counts larger than an int discard the whole sequence rather than wrapping.
func (d *Decoder) Feed(k Key) (Command, bool) {
	if k.Special == Esc {
		d.reset()
		return Command{}, false
	}
	if d.mode != Normal {
		return d.feedPrompt(k)
	}
	if d.prefix == (Key{}) && k.Special == NoSpecial && !k.Ctrl && k.Rune >= '0' && k.Rune <= '9' {
		if len(d.digits) == 0 && k.Rune == '0' {
			return Command{}, false
		}
		digit := int(k.Rune - '0')
		if d.count > (int(^uint(0)>>1)-digit)/10 {
			d.countTooBig = true
		} else if !d.countTooBig {
			d.count = d.count*10 + digit
		}
		d.digits = append(d.digits, byte(k.Rune))
		return Command{}, false
	}
	for _, b := range bindings {
		if d.prefix != (Key{}) {
			if b.sequence[0] != d.prefix || !b.hasSecondKey() || !b.matchesSecond(k) {
				continue
			}
		} else {
			if b.sequence[0] != k {
				continue
			}
			if b.hasSecondKey() {
				d.prefix = k
				return Command{}, false
			}
		}
		cmd := Command{Action: b.action, Count: d.count, Arg: b.arg}
		if b.countSign != 0 {
			if cmd.Count == 0 {
				cmd.Count = 1
			}
			cmd.Count *= b.countSign
		}
		if cmd.Count != 0 && b.counted != NoAction {
			cmd.Action = b.counted
		}
		if b.mark {
			cmd.Arg = k.Rune
		}
		tooBig := d.countTooBig
		d.reset()
		if tooBig {
			return Command{}, false
		}
		if b.prompt != Normal {
			d.mode = b.prompt
			d.promptStart = k.Rune
			d.searchAction = b.action
			return Command{}, false
		}
		return cmd, true
	}
	d.reset()
	return Command{}, false
}

func (d *Decoder) feedPrompt(k Key) (Command, bool) {
	switch k.Special {
	case Backspace:
		if len(d.prompt) == 0 {
			d.reset()
		} else {
			d.prompt = d.prompt[:len(d.prompt)-1]
		}
	case Enter:
		var cmd Command
		if d.mode == Search {
			cmd = Command{Action: d.searchAction, Text: string(d.prompt)}
		} else {
			line := string(d.prompt)
			if strings.TrimSpace(line) == "" {
				d.reset()
				return Command{}, false
			}
			var err error
			cmd, err = ParseCommandLine(line)
			if err != nil {
				return Command{}, false
			}
		}
		d.reset()
		return cmd, true
	case NoSpecial:
		if !k.Ctrl && unicode.IsPrint(k.Rune) {
			d.prompt = append(d.prompt, k.Rune)
		}
	}
	return Command{}, false
}

func (d *Decoder) reset() {
	d.mode = Normal
	d.prefix = Key{}
	d.digits = d.digits[:0]
	d.count = 0
	d.countTooBig = false
	d.prompt = d.prompt[:0]
	d.promptStart = 0
	d.searchAction = NoAction
}

// Help returns a fresh slice generated from the decoder's binding table and
// the command-line table. {N} is a positive count; {a-z} is one lowercase letter.
func Help() []HelpEntry {
	entries := make([]HelpEntry, 0, len(bindings)+len(lineBindings)+2)
	for _, b := range bindings {
		text := keyText(b.sequence[0])
		if b.mark {
			text += "{a-z}"
		} else if b.hasSecondKey() {
			text += keyText(b.sequence[1])
		}
		entries = append(entries, HelpEntry{text, b.action, b.description})
		if b.counted != NoAction {
			entries = append(entries, HelpEntry{"{N}" + text, b.counted, b.description})
		}
	}
	for _, b := range lineBindings {
		entries = append(entries, HelpEntry{":" + b.helpKeys, b.action, b.description})
	}
	return entries
}

func keyText(k Key) string {
	if k.Special != NoSpecial {
		names := [...]string{"", "Up", "Down", "Left", "Right", "PageUp", "PageDown", "Esc", "Enter", "Backspace", "Tab", "F1", "Home", "End"}
		if int(k.Special) > 0 && int(k.Special) < len(names) {
			return names[k.Special]
		}
		return "Special(" + strconv.Itoa(int(k.Special)) + ")"
	}
	if k.Ctrl {
		return "Ctrl-" + string(k.Rune)
	}
	if k.Rune == ' ' {
		return "Space"
	}
	return string(k.Rune)
}
