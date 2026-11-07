package keys

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func textKeys(text string) []Key {
	var result []Key
	for _, r := range text {
		result = append(result, Key{Rune: r})
	}
	return result
}

func ending(text string, special Special) []Key {
	return append(textKeys(text), Key{Special: special})
}

func TestNormalInput(t *testing.T) {
	tests := []struct {
		name  string
		input []Key
		want  []Command
	}{
		{"counted scroll", textKeys("5j"), []Command{{Action: ScrollDown, Count: 5}}},
		{"page target", textKeys("12G"), []Command{{Action: GoToPage, Count: 12}}},
		{"last page", textKeys("G"), []Command{{Action: LastPage}}},
		{"first page", textKeys("gg"), []Command{{Action: FirstPage}}},
		{"tab target", textKeys("3gt"), []Command{{Action: GoToTab, Count: 3}}},
		{"move left", textKeys("g<"), []Command{{Action: MoveTab, Count: -1, Arg: '-'}}},
		{"move right", textKeys("g>"), []Command{{Action: MoveTab, Count: 1, Arg: '+'}}},
		{"move several tabs left", textKeys("3g<"), []Command{{Action: MoveTab, Count: -3, Arg: '-'}}},
		{"move several tabs right", textKeys("12g>"), []Command{{Action: MoveTab, Count: 12, Arg: '+'}}},
		{"move count resets", textKeys("3g<g>"), []Command{{Action: MoveTab, Count: -3, Arg: '-'}, {Action: MoveTab, Count: 1, Arg: '+'}}},
		{"bad prefix consumes unknown key", textKeys("gjk"), []Command{{Action: ScrollUp}}},
		{"bad prefix resets count", textKeys("12gxj"), []Command{{Action: ScrollDown}}},
		{"bad normal key resets count", textKeys("12xj"), []Command{{Action: ScrollDown}}},
		{"command resets count", textKeys("5jk"), []Command{{Action: ScrollDown, Count: 5}, {Action: ScrollUp}}},
		{"set mark", textKeys("ma"), []Command{{Action: SetMark, Arg: 'a'}}},
		{"jump to mark", textKeys("'a"), []Command{{Action: JumpToMark, Arg: 'a'}}},
		{"jump back", textKeys("''"), []Command{{Action: JumpBack}}},
		{"last mark", textKeys("mz'z"), []Command{{Action: SetMark, Arg: 'z'}, {Action: JumpToMark, Arg: 'z'}}},
		{"invalid mark resets", textKeys("mAj"), []Command{{Action: ScrollDown}}},
		{"leading zero is not a count", textKeys("0j"), []Command{{Action: ScrollDown}}},
		{"zero within count", textKeys("100J"), []Command{{Action: NextPage, Count: 100}}},
		{"largest count", textKeys(strconv.Itoa(int(^uint(0)>>1)) + "j"), []Command{{Action: ScrollDown, Count: int(^uint(0) >> 1)}}},
		{"overflow drops sequence", textKeys(strconv.Itoa(int(^uint(0)>>1)) + "0jk"), []Command{{Action: ScrollUp}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := NewDecoder()
			var got []Command
			for _, k := range test.input {
				if cmd, ok := d.Feed(k); ok {
					got = append(got, cmd)
				}
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("commands = %+v, want %+v", got, test.want)
			}
			if d.Pending() != "" || d.Mode() != Normal {
				t.Fatalf("after command: pending %q, mode %v", d.Pending(), d.Mode())
			}
		})
	}
}

func TestDefaultBindings(t *testing.T) {
	tests := []struct {
		input string
		want  Action
	}{
		{"j", ScrollDown}, {"k", ScrollUp}, {"h", ScrollLeft}, {"l", ScrollRight},
		{" ", FullPageDown}, {"b", FullPageUp}, {"J", NextPage}, {"K", PreviousPage},
		{"H", ScrollToTop}, {"M", ScrollToMiddle}, {"L", ScrollToBottom},
		{"+", ZoomIn}, {"zi", ZoomIn}, {"-", ZoomOut}, {"zo", ZoomOut},
		{"zw", FitWidth}, {"zp", FitPage}, {"zz", FitPage}, {"z0", ResetZoom},
		{"gt", NextTab}, {"gT", PreviousTab}, {"gd", DuplicateTab}, {"]b", NextTab}, {"[b", PreviousTab},
		{"o", OpenFile}, {"q", CloseTab}, {"Q", QuitAll},
		{"n", SearchNext}, {"N", SearchPrevious}, {"r", Reload}, {"gO", Outline},
		{"gb", TabList}, {"gp", PreviousUsedTab}, {"zd", ToggleDark}, {"gs", ToggleSidebar},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			var d Decoder
			input := textKeys(test.input)
			for i, k := range input {
				got, ok := d.Feed(k)
				if i < len(input)-1 {
					if ok {
						t.Fatalf("incomplete sequence emitted %+v", got)
					}
				} else if !ok || got != (Command{Action: test.want}) {
					t.Fatalf("Feed = %+v, %v; want action %v", got, ok, test.want)
				}
			}
		})
	}
	keyTests := []struct {
		key  Key
		want Action
	}{
		{Key{Special: Down}, ScrollDown}, {Key{Special: Up}, ScrollUp},
		{Key{Special: Left}, ScrollLeft}, {Key{Special: Right}, ScrollRight},
		{Key{Special: PageDown}, NextPage}, {Key{Special: PageUp}, PreviousPage},
		{Key{Special: Home}, FirstPage}, {Key{Special: End}, LastPage},
		{Key{Special: F1}, HelpAction}, {Key{Special: Tab}, JumpNewer},
		{Key{Rune: 'd', Ctrl: true}, HalfPageDown}, {Key{Rune: 'u', Ctrl: true}, HalfPageUp},
		{Key{Rune: 'f', Ctrl: true}, FullPageDown}, {Key{Rune: 'b', Ctrl: true}, FullPageUp},
		{Key{Rune: 'o', Ctrl: true}, JumpOlder}, {Key{Rune: 'i', Ctrl: true}, JumpNewer},
		{Key{Rune: 'l', Ctrl: true}, Redraw},
		{Key{Rune: '^', Ctrl: true}, PreviousUsedTab},
	}
	for _, test := range keyTests {
		t.Run(keyText(test.key), func(t *testing.T) {
			var d Decoder
			got, ok := d.Feed(test.key)
			if !ok || got != (Command{Action: test.want}) {
				t.Fatalf("Feed = %+v, %v; want action %v", got, ok, test.want)
			}
		})
	}
}

func TestPendingAndEscape(t *testing.T) {
	for _, input := range []string{"12", "12g", "z", "m", "'", ":tabn", "/qubit", "?qubit"} {
		t.Run(input, func(t *testing.T) {
			var d Decoder
			for _, k := range textKeys(input) {
				if cmd, ok := d.Feed(k); ok {
					t.Fatalf("pending input emitted %+v", cmd)
				}
			}
			if d.Pending() != input {
				t.Fatalf("Pending = %q, want %q", d.Pending(), input)
			}
			if cmd, ok := d.Feed(Key{Special: Esc}); ok || cmd != (Command{}) {
				t.Fatalf("Esc emitted %+v, %v", cmd, ok)
			}
			if d.Pending() != "" || d.Mode() != Normal {
				t.Fatalf("Esc left pending %q, mode %v", d.Pending(), d.Mode())
			}
			if cmd, ok := d.Feed(Key{Rune: 'j'}); !ok || cmd != (Command{Action: ScrollDown}) {
				t.Fatalf("after Esc = %+v, %v", cmd, ok)
			}
		})
	}
}

func TestPrompts(t *testing.T) {
	tests := []struct {
		name  string
		input []Key
		want  Command
	}{
		{"forward search", ending("/qubit", Enter), Command{Action: SearchForward, Text: "qubit"}},
		{"backward search", ending("?qubit", Enter), Command{Action: SearchBackward, Text: "qubit"}},
		{"empty search repeats", ending("/", Enter), Command{Action: SearchForward}},
		{"literal search spaces", ending("/two words", Enter), Command{Action: SearchForward, Text: "two words"}},
		{"unicode search", ending("/量子 λ", Enter), Command{Action: SearchForward, Text: "量子 λ"}},
		{"open path", ending(":e my file.pdf", Enter), Command{Action: OpenFile, Text: "my file.pdf"}},
		{"page command", ending(":12", Enter), Command{Action: GoToPage, Count: 12}},
		{"duplicate command", ending(":tab split", Enter), Command{Action: DuplicateTab}},
		{"count does not leak into prompt", ending("5/qubit", Enter), Command{Action: SearchForward, Text: "qubit"}},
		{"backspace removes rune", append(ending("/量λ", Backspace), Key{Special: Enter}), Command{Action: SearchForward, Text: "量"}},
		{"editing command", append(ending(":qa", Backspace), Key{Special: Enter}), Command{Action: CloseTab}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var d Decoder
			for i, k := range test.input {
				got, ok := d.Feed(k)
				if i < len(test.input)-1 {
					if ok {
						t.Fatalf("prompt emitted early: %+v", got)
					}
				} else if !ok || got != test.want {
					t.Fatalf("submission = %+v, %v; want %+v", got, ok, test.want)
				}
			}
			if d.Mode() != Normal || d.Pending() != "" {
				t.Fatalf("submission left mode %v, pending %q", d.Mode(), d.Pending())
			}
		})
	}
}

func TestPromptEditingAndModes(t *testing.T) {
	for _, starter := range []rune{':', '/', '?'} {
		t.Run(string(starter), func(t *testing.T) {
			var d Decoder
			d.Feed(Key{Rune: starter})
			wantMode := Search
			if starter == ':' {
				wantMode = CommandLine
			}
			if d.Mode() != wantMode {
				t.Fatalf("Mode = %v, want %v", d.Mode(), wantMode)
			}
			d.Feed(Key{Rune: 'λ'})
			d.Feed(Key{Special: Backspace})
			if d.Mode() != wantMode || d.Pending() != string(starter) {
				t.Fatalf("deleting final rune left mode %v, pending %q", d.Mode(), d.Pending())
			}
			d.Feed(Key{Special: Backspace})
			if d.Mode() != Normal || d.Pending() != "" {
				t.Fatalf("empty Backspace left mode %v, pending %q", d.Mode(), d.Pending())
			}
			if cmd, ok := d.Feed(Key{Rune: 'j'}); !ok || cmd.Action != ScrollDown {
				t.Fatalf("normal key after Backspace = %+v, %v", cmd, ok)
			}
		})
	}
	var d Decoder
	for _, k := range ending(":frobnicate", Enter) {
		if cmd, ok := d.Feed(k); ok {
			t.Fatalf("invalid command emitted %+v", cmd)
		}
	}
	if d.Mode() != CommandLine || d.Pending() != ":frobnicate" {
		t.Fatalf("invalid command was not kept for editing: %v, %q", d.Mode(), d.Pending())
	}
	d.Feed(Key{Special: Esc})
	d.Feed(Key{Rune: ':'})
	if _, ok := d.Feed(Key{Special: Enter}); ok || d.Mode() != Normal {
		t.Fatal("empty command line did not close without a command")
	}
	d.Feed(Key{Rune: '/'})
	for _, k := range []Key{{Special: Left}, {Special: Tab}, {Rune: 'd', Ctrl: true}, {Rune: '\n'}} {
		d.Feed(k)
	}
	if d.Pending() != "/" {
		t.Fatalf("non-text keys changed search: %q", d.Pending())
	}
}

func TestParseCommandLine(t *testing.T) {
	tests := []struct {
		line string
		want Command
	}{
		{":e my file.pdf", Command{Action: OpenFile, Text: "my file.pdf"}},
		{"e my  file.pdf ", Command{Action: OpenFile, Text: "my  file.pdf "}},
		{":e \"my file.pdf\"", Command{Action: OpenFile, Text: "\"my file.pdf\""}},
		{`:e my\ file.pdf`, Command{Action: OpenFile, Text: `my\ file.pdf`}},
		{":e ./ leading.pdf", Command{Action: OpenFile, Text: "./ leading.pdf"}},
		{":e 文書.pdf", Command{Action: OpenFile, Text: "文書.pdf"}},
		{" \t: e\tmy file.pdf", Command{Action: OpenFile, Text: "my file.pdf"}},
		{":12", Command{Action: GoToPage, Count: 12}},
		{"12 ", Command{Action: GoToPage, Count: 12}},
		{":q", Command{Action: CloseTab}}, {":qa", Command{Action: QuitAll}},
		{":tabnew", Command{Action: NewTab}}, {":tabclose", Command{Action: CloseTab}},
		{":tab split", Command{Action: DuplicateTab}}, {":tab\t split ", Command{Action: DuplicateTab}},
		{":help", Command{Action: HelpAction}}, {":outline", Command{Action: Outline}},
		{":reload", Command{Action: Reload}}, {":mark a", Command{Action: SetMark, Arg: 'a'}},
		{":ls", Command{Action: TabList}}, {":tabs", Command{Action: TabList}},
		{":buffers", Command{Action: TabList}}, {":b", Command{Action: TabList}},
		{":b 12", Command{Action: GoToTab, Count: 12}},
		{":b  my file.pdf  ", Command{Action: GoToTabByName, Text: "my file.pdf"}},
		{":e %", Command{Action: DuplicateTab, Text: "%"}},
		{":e % ", Command{Action: OpenFile, Text: "% "}},
		{":e ./%", Command{Action: OpenFile, Text: "./%"}},
		{":tabnew %", Command{Action: DuplicateTab, Text: "%"}},
		{":tabnew %  ", Command{Action: DuplicateTab, Text: "%"}},
		{":tabmove 0", Command{Action: MoveTab}},
		{":tabmove 3", Command{Action: MoveTab, Count: 3}},
		{":tabmove +2", Command{Action: MoveTab, Count: 2, Arg: '+'}},
		{":tabmove -2", Command{Action: MoveTab, Count: -2, Arg: '-'}},
		{":tabmove +0", Command{Action: MoveTab, Arg: '+'}},
		{":tabmove -0", Command{Action: MoveTab, Arg: '-'}},
		{":tabonly", Command{Action: CloseOtherTabs}},
		{":bn", Command{Action: NextTab}}, {":tabnext", Command{Action: NextTab}},
		{":bp", Command{Action: PreviousTab}}, {":tabp", Command{Action: PreviousTab}},
		{":dark", Command{Action: ToggleDark}}, {":sidebar", Command{Action: ToggleSidebar}},
	}
	for _, test := range tests {
		t.Run(test.line, func(t *testing.T) {
			got, err := ParseCommandLine(test.line)
			if err != nil || got != test.want {
				t.Fatalf("ParseCommandLine = %+v, %v; want %+v", got, err, test.want)
			}
		})
	}
	for _, line := range []string{":frobnicate", ":0", ":12 extra", ":9999999999999999999999999999", ":q extra", ":e", ":tab", ":tab dance", ":mark A", ":mark ab", ":", "", ":b 0", ":b 9999999999999999999999999999", ":tabnew file.pdf", ":tabmove", ":tabmove +", ":tabmove --1", ":tabmove 1 2", ":tabmove 9999999999999999999999999999", ":tabonly extra"} {
		t.Run("invalid "+line, func(t *testing.T) {
			cmd, err := ParseCommandLine(line)
			if err == nil || cmd != (Command{}) {
				t.Fatalf("invalid input returned %+v, %v", cmd, err)
			}
			if line == ":frobnicate" && !strings.Contains(err.Error(), "frobnicate") {
				t.Fatalf("unknown command missing from error: %v", err)
			}
		})
	}
}

func TestBindingSequencesAreUnambiguous(t *testing.T) {
	prefixes := map[Key]bool{{Rune: 'g'}: true, {Rune: 'z'}: true, {Rune: 'm'}: true, {Rune: '\''}: true, {Rune: ']'}: true, {Rune: '['}: true}
	var sequences [][]Key
	for _, b := range bindings {
		if b.mark {
			for r := 'a'; r <= 'z'; r++ {
				sequences = append(sequences, []Key{b.sequence[0], {Rune: r}})
			}
		} else if b.hasSecondKey() {
			sequences = append(sequences, []Key{b.sequence[0], b.sequence[1]})
		} else {
			sequences = append(sequences, []Key{b.sequence[0]})
		}
	}
	for i, a := range sequences {
		if len(a) > 1 && !prefixes[a[0]] {
			t.Fatalf("unregistered prefix in %v", a)
		}
		for j, b := range sequences {
			if i == j {
				continue
			}
			if reflect.DeepEqual(a, b) {
				t.Fatalf("duplicate binding sequence %v", a)
			}
			if len(a) < len(b) && reflect.DeepEqual(a, b[:len(a)]) && !(len(a) == 1 && prefixes[a[0]]) {
				t.Fatalf("sequence %v is an unregistered prefix of %v", a, b)
			}
		}
	}
}

func TestHelpCoversActions(t *testing.T) {
	entries := Help()
	actions := make(map[Action]bool)
	spellings := make(map[string]bool)
	for _, entry := range entries {
		if entry.Keys == "" || entry.Description == "" || entry.Action == NoAction {
			t.Fatalf("incomplete help entry: %+v", entry)
		}
		if spellings[entry.Keys] {
			t.Fatalf("duplicate help spelling %q", entry.Keys)
		}
		spellings[entry.Keys] = true
		actions[entry.Action] = true
	}
	for action := ScrollDown; action <= OpenCommandLine; action++ {
		if !actions[action] {
			t.Errorf("action %v has no help entry", action)
		}
	}
	for _, text := range []string{"{N}G", "{N}gt", "m{a-z}", "'{a-z}", "''", "F1", "?", ":tab split", ":tabnew [%]", ":e {path|%}", "gb", "gp", "Ctrl-^", "g<", "g>", "zd", "gs", ":ls", ":tabs", ":buffers", ":b [N|name]", ":tabmove {N|+N|-N}", ":tabonly", ":dark", ":sidebar"} {
		if !spellings[text] {
			t.Errorf("missing help for %q", text)
		}
	}
	entries[0].Keys = "changed"
	if Help()[0].Keys == "changed" {
		t.Fatal("caller changed future Help results")
	}
}

func ExampleDecoder() {
	d := NewDecoder()
	for _, r := range "12G" {
		if cmd, ok := d.Feed(Key{Rune: r}); ok {
			fmt.Println(cmd.Action == GoToPage, cmd.Count)
		}
	}
	for _, r := range "/qubit" {
		d.Feed(Key{Rune: r})
	}
	fmt.Println(d.Mode() == Search, d.Pending())
	cmd, ok := d.Feed(Key{Special: Enter})
	fmt.Println(ok, cmd.Action == SearchForward, cmd.Text)
	// Output:
	// true 12
	// true /qubit
	// true true qubit
}
