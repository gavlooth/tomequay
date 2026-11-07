package terminal

import (
	"reflect"
	"strings"
	"testing"

	"terminal-pdf-reader/internal/keys"
)

func keyEvents(values ...keys.Key) []Event {
	var result []Event
	for _, key := range values {
		result = append(result, KeyEvent{Key: key})
	}
	return result
}

func TestParse(t *testing.T) {
	special := func(s keys.Special) keys.Key { return keys.Key{Special: s} }
	tests := []struct {
		name  string
		input string
		want  []Event
	}{
		{"ASCII", "qQ /", keyEvents(keys.Key{Rune: 'q'}, keys.Key{Rune: 'Q'}, keys.Key{Rune: ' '}, keys.Key{Rune: '/'})},
		{"CSI arrows", "\x1b[A\x1b[B\x1b[C\x1b[D", keyEvents(special(keys.Up), special(keys.Down), special(keys.Right), special(keys.Left))},
		{"SS3 arrows", "\x1bOA\x1bOB\x1bOC\x1bOD", keyEvents(special(keys.Up), special(keys.Down), special(keys.Right), special(keys.Left))},
		{"home end", "\x1b[H\x1b[F\x1bOH\x1bOF\x1b[1~\x1b[4~\x1b[7~\x1b[8~", keyEvents(special(keys.Home), special(keys.End), special(keys.Home), special(keys.End), special(keys.Home), special(keys.End), special(keys.Home), special(keys.End))},
		{"page and help", "\x1b[5~\x1b[6~\x1bOP\x1b[11~", keyEvents(special(keys.PageUp), special(keys.PageDown), special(keys.F1), special(keys.F1))},
		{"control letters", "\x01\x02\x03\x04\x05\x06\x07\x0b\x0c\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a", keyEvents(ctrl('a'), ctrl('b'), ctrl('c'), ctrl('d'), ctrl('e'), ctrl('f'), ctrl('g'), ctrl('k'), ctrl('l'), ctrl('n'), ctrl('o'), ctrl('p'), ctrl('q'), ctrl('r'), ctrl('s'), ctrl('t'), ctrl('u'), ctrl('v'), ctrl('w'), ctrl('x'), ctrl('y'), ctrl('z'))},
		{"control caret", "\x1ej", keyEvents(keys.Key{Rune: '^', Ctrl: true}, keys.Key{Rune: 'j'})},
		{"whitespace and erase", "\x7f\x08\r\n\t", keyEvents(special(keys.Backspace), special(keys.Backspace), special(keys.Enter), special(keys.Enter), special(keys.Tab))},
		{"Alt", "\x1bx\x1b界", keyEvents(special(keys.Esc), keys.Key{Rune: 'x'}, special(keys.Esc), keys.Key{Rune: '界'})},
		{"Unicode", "é界🙂", keyEvents(keys.Key{Rune: 'é'}, keys.Key{Rune: '界'}, keys.Key{Rune: '🙂'})},
		{"cell reply", "\x1b[6;21;9tx", []Event{cellSizeReply{height: 21, width: 9}, KeyEvent{Key: keys.Key{Rune: 'x'}}}},
		{"invalid replies", "\x1b[6;0;9t\x1b[6;21;0t\x1b[6;1;2;3t", nil},
		{"legacy mouse and focus", "\x1b[M !\"\x1b[I\x1b[Oq", keyEvents(keys.Key{Rune: 'q'})},
		{"mouse left press", "\x1b[<0;12;4M", []Event{MouseEvent{Kind: MousePress, Button: ButtonLeft, Column: 11, Row: 3}}},
		{"mouse middle press", "\x1b[<1;1;1M", []Event{MouseEvent{Kind: MousePress, Button: ButtonMiddle}}},
		{"mouse right press", "\x1b[<2;2;3M", []Event{MouseEvent{Kind: MousePress, Button: ButtonRight, Column: 1, Row: 2}}},
		{"mouse release", "\x1b[<0;12;4m\x1b[<1;2;3m\x1b[<2;1;1m\x1b[<3;1;1m", []Event{
			MouseEvent{Kind: MouseRelease, Button: ButtonLeft, Column: 11, Row: 3},
			MouseEvent{Kind: MouseRelease, Button: ButtonMiddle, Column: 1, Row: 2},
			MouseEvent{Kind: MouseRelease, Button: ButtonRight},
			MouseEvent{Kind: MouseRelease, Button: ButtonNone},
		}},
		{"mouse wheel", "\x1b[<64;1;1M\x1b[<65;1;1M\x1b[<66;1;1M\x1b[<67;1;1M", []Event{
			MouseEvent{Kind: MouseWheelUp, Button: ButtonNone},
			MouseEvent{Kind: MouseWheelDown, Button: ButtonNone},
			MouseEvent{Kind: MouseWheelLeft, Button: ButtonNone},
			MouseEvent{Kind: MouseWheelRight, Button: ButtonNone},
		}},
		{"mouse shift", "\x1b[<4;1;1M", []Event{MouseEvent{Kind: MousePress, Button: ButtonLeft, Shift: true}}},
		{"mouse alt", "\x1b[<8;1;1M", []Event{MouseEvent{Kind: MousePress, Button: ButtonLeft, Alt: true}}},
		{"mouse ctrl", "\x1b[<16;1;1M", []Event{MouseEvent{Kind: MousePress, Button: ButtonLeft, Ctrl: true}}},
		{"mouse all modifiers", "\x1b[<30;1;1m", []Event{MouseEvent{Kind: MouseRelease, Button: ButtonRight, Shift: true, Alt: true, Ctrl: true}}},
		{"mouse wheel modifiers", "\x1b[<93;1;1M", []Event{MouseEvent{Kind: MouseWheelDown, Button: ButtonNone, Shift: true, Alt: true, Ctrl: true}}},
		{"mouse drag", "\x1b[<32;14;9M\x1b[<33;1;1M\x1b[<62;1;1M", []Event{
			MouseEvent{Kind: MouseDrag, Button: ButtonLeft, Column: 13, Row: 8},
			MouseEvent{Kind: MouseDrag, Button: ButtonMiddle},
			MouseEvent{Kind: MouseDrag, Button: ButtonRight, Shift: true, Alt: true, Ctrl: true},
		}},
		{"mouse large coordinates", "\x1b[<0;4096;1024Mq", []Event{
			MouseEvent{Kind: MousePress, Button: ButtonLeft, Column: 4095, Row: 1023},
			KeyEvent{Key: keys.Key{Rune: 'q'}},
		}},
		{"mouse missing fields", "\x1b[<M\x1b[<0;1M\x1b[<;1;1M\x1b[<0;;1M\x1b[<0;1;Mq", keyEvents(keys.Key{Rune: 'q'})},
		{"mouse invalid fields", "\x1b[<0;0;1M\x1b[<0;1;0M\x1b[<0;-1;1M\x1b[<0;+1;1M\x1b[<0;1;1;2Mq", keyEvents(keys.Key{Rune: 'q'})},
		{"mouse overflowing fields", "\x1b[<999999999999999999999;1;1M\x1b[<0;999999999999999999999;1M\x1b[<0;1;999999999999999999999Mq", keyEvents(keys.Key{Rune: 'q'})},
		{"mouse unsupported actions", "\x1b[<128;1;1M\x1b[<3;1;1M\x1b[<64;1;1m\x1b[<96;1;1M\x1b[<32;1;1mq", keyEvents(keys.Key{Rune: 'q'})},
		{"mouse wrong final", "\x1b[<0;1;1Aq", keyEvents(keys.Key{Rune: 'q'})},
		{"mouse interrupted", "\x1b[<0;12;\x1b[Aq", keyEvents(special(keys.Up), keys.Key{Rune: 'q'})},
		{"paste", "\x1b[200~q\x03界\x1b[A\x1b[201~j", keyEvents(keys.Key{Rune: 'j'})},
		{"control strings", "\x1b]title\a\x1bPabc\x1b\\\x1b_Greply\x1b\\j", keyEvents(keys.Key{Rune: 'j'})},
		{"graphics success", "\x1b_Gi=123;OK\x1b\\", []Event{GraphicsReplyEvent{ID: 123, OK: true, Message: "OK"}}},
		{"graphics error", "\x1b_Gi=4294967295,p=2;ENOENT: no image\x1b\\j", []Event{GraphicsReplyEvent{ID: 4294967295, Message: "ENOENT: no image"}, KeyEvent{Key: keys.Key{Rune: 'j'}}}},
		{"graphics multiple", "\x1b_Gi=1;OK\x1b\\\x1b_Gi=2;EINVAL\x1b\\", []Event{GraphicsReplyEvent{ID: 1, OK: true, Message: "OK"}, GraphicsReplyEvent{ID: 2, Message: "EINVAL"}}},
		{"graphics malformed", "\x1b_Gi=0;OK\x1b\\\x1b_Gi=-1;OK\x1b\\\x1b_Gi=4294967296;OK\x1b\\\x1b_Gi=1,i=2;OK\x1b\\\x1b_Gp=1;OK\x1b\\\x1b_Gi=1;\x1b\\\x1b_Gi=1;OK\a", nil},
		{"graphics control injection", "\x1b_Gi=1;bad\x1b[31m\x1b\\", nil},
		{"unknown sequences", "\x1b[99~\x1b[?25h\x1bOQq", keyEvents(keys.Key{Rune: 'q'})},
		{"modified arrow", "\x1b[1;5A", keyEvents(keys.Key{Special: keys.Up, Ctrl: true})},
		{"invalid UTF-8", "\xffj", keyEvents(keys.Key{Rune: 'j'})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Every possible two-read split, including inside UTF-8 and replies.
			for split := range len(test.input) + 1 {
				state, first := parse(parserState{}, []byte(test.input[:split]), false)
				state, second := parse(state, []byte(test.input[split:]), false)
				got := append(first, second...)
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("split %d: got %#v, want %#v", split, got, test.want)
				}
				if state != (parserState{}) {
					t.Fatalf("split %d: unfinished state %#v", split, state)
				}
			}
			state := parserState{}
			var got []Event
			for i := range len(test.input) {
				var next []Event
				state, next = parse(state, []byte(test.input[i:i+1]), false)
				got = append(got, next...)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("byte reads: got %#v, want %#v", got, test.want)
			}
		})
	}
}

func ctrl(r rune) keys.Key { return keys.Key{Rune: r, Ctrl: true} }

func TestEscapeTimeout(t *testing.T) {
	state, events := parse(parserState{}, []byte("\x1b"), false)
	if len(events) != 0 || state.pending != "\x1b" {
		t.Fatalf("early escape: %#v %#v", state, events)
	}
	state, events = parse(state, nil, true)
	if state != (parserState{}) || !reflect.DeepEqual(events, keyEvents(keys.Key{Special: keys.Esc})) {
		t.Fatalf("timeout: %#v %#v", state, events)
	}
	state, events = parse(parserState{}, []byte("\x1b["), true)
	if len(events) != 0 {
		t.Fatalf("partial CSI became keys: %#v", events)
	}
	_, events = parse(state, []byte("A"), false)
	if !reflect.DeepEqual(events, keyEvents(keys.Key{Special: keys.Up})) {
		t.Fatalf("split CSI: %#v", events)
	}
	state, _ = parse(parserState{}, []byte("\xc3"), false)
	state, events = parse(state, nil, true)
	if len(events) != 0 || state.pending != "\xc3" {
		t.Fatalf("UTF-8 changed on timeout: %#v %#v", state, events)
	}
}

func TestGraphicsReplyLimit(t *testing.T) {
	state, events := parse(parserState{}, []byte("\x1b_Gi=1;"), false)
	if len(events) != 0 {
		t.Fatalf("incomplete reply emitted events: %#v", events)
	}
	state, events = parse(state, []byte(strings.Repeat("x", 5000)), false)
	if len(events) != 0 || len(state.body) > 4096 {
		t.Fatalf("oversized reply retained: %#v, events %#v", state, events)
	}
	state, events = parse(state, []byte("\x1b\\\x1b_Gi=2;OK\x1b\\q"), false)
	want := []Event{GraphicsReplyEvent{ID: 2, OK: true, Message: "OK"}, KeyEvent{Key: keys.Key{Rune: 'q'}}}
	if state != (parserState{}) || !reflect.DeepEqual(events, want) {
		t.Fatalf("recovery after oversized reply: %#v, events %#v", state, events)
	}
}
