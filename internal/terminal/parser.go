package terminal

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"terminal-pdf-reader/internal/keys"
)

// Home and End extend keys.Special for terminal navigation keys.
const (
	Home keys.Special = keys.F1 + 1 + iota
	End
)

type parserState struct {
	pending string
	paste   bool
	control bool
}

type cellSizeReply struct{ height, width int }

// parse is a pure streaming decoder: the returned state is the only state needed
// by the next call. timeout means no byte followed a lone Escape within 25 ms.
// Terminal replies are private events, routed to the size query by the reader.
func parse(state parserState, input []byte, timeout bool) (parserState, []Event) {
	text := state.pending + string(input)
	state.pending = ""
	var events []Event
	emit := func(key keys.Key) { events = append(events, KeyEvent{Key: key}) }
	for len(text) > 0 {
		if state.paste {
			if end := strings.Index(text, "\x1b[201~"); end >= 0 {
				text = text[end+6:]
				state.paste = false
				continue
			}
			state.pending = suffixPrefix(text, "\x1b[201~")
			return state, events
		}
		if state.control {
			end := -1
			for i := range len(text) {
				if text[i] == '\a' {
					end = i + 1
					break
				}
				if text[i] == '\x1b' && i+1 < len(text) && text[i+1] == '\\' {
					end = i + 2
					break
				}
			}
			if end < 0 {
				state.pending = suffixPrefix(text, "\x1b\\")
				return state, events
			}
			state.control = false
			text = text[end:]
			continue
		}
		if text[0] == '\x1b' {
			if len(text) == 1 {
				if timeout {
					emit(keys.Key{Special: keys.Esc})
				} else {
					state.pending = text
				}
				return state, events
			}
			switch text[1] {
			case ']', 'P', '_', '^', 'X':
				state.control = true
				text = text[2:]
				continue
			case '[', 'O':
				end := 2
				for end < len(text) && (text[end] < 0x40 || text[end] > 0x7e) {
					end++
				}
				if end == len(text) {
					// Bound malformed control-sequence buffering; still consume its
					// eventual final byte rather than interpreting it as a key.
					if len(text) > 4096 {
						text = text[:2] + text[len(text)-32:]
					}
					state.pending = text
					return state, events
				}
				sequence := text[2 : end+1]
				if text[1] == '[' && sequence == "M" {
					// Legacy X10 mouse reports have three bytes after CSI M.
					if len(text) < end+4 {
						state.pending = text
						return state, events
					}
					text = text[end+4:]
					continue
				}
				text = text[end+1:]
				if sequence == "200~" {
					state.paste = true
					continue
				}
				if reply, ok := parseCellSize(sequence); ok {
					events = append(events, reply)
					continue
				}
				if key, ok := sequenceKey(sequence); ok {
					emit(key)
				}
				continue
			default:
				// Key has no Alt flag: preserve the Escape-prefixed spelling.
				if !utf8.FullRuneInString(text[1:]) {
					state.pending = text
					return state, events
				}
				emit(keys.Key{Special: keys.Esc})
				text = text[1:]
				continue
			}
		}
		b := text[0]
		if b < 0x20 || b == 0x7f {
			text = text[1:]
			switch b {
			case 8, 0x7f:
				emit(keys.Key{Special: keys.Backspace})
			case 9:
				emit(keys.Key{Special: keys.Tab})
			case 10, 13:
				emit(keys.Key{Special: keys.Enter})
			default:
				if b >= 1 && b <= 26 {
					emit(keys.Key{Rune: rune('a' + b - 1), Ctrl: true})
				}
			}
			continue
		}
		if !utf8.FullRuneInString(text) {
			state.pending = text
			return state, events
		}
		r, n := utf8.DecodeRuneInString(text)
		text = text[n:]
		if r != utf8.RuneError || n != 1 {
			emit(keys.Key{Rune: r})
		}
	}
	return state, events
}

func suffixPrefix(text, delimiter string) string {
	for n := min(len(text), len(delimiter)-1); n > 0; n-- {
		if strings.HasSuffix(text, delimiter[:n]) {
			return text[len(text)-n:]
		}
	}
	return ""
}

func parseCellSize(sequence string) (cellSizeReply, bool) {
	if !strings.HasPrefix(sequence, "6;") || !strings.HasSuffix(sequence, "t") {
		return cellSizeReply{}, false
	}
	h, w, ok := strings.Cut(sequence[2:len(sequence)-1], ";")
	if !ok {
		return cellSizeReply{}, false
	}
	height, eh := strconv.Atoi(h)
	width, ew := strconv.Atoi(w)
	return cellSizeReply{height: height, width: width}, eh == nil && ew == nil && height > 0 && width > 0
}

func sequenceKey(sequence string) (keys.Key, bool) {
	final := sequence[len(sequence)-1]
	params := sequence[:len(sequence)-1]
	key := keys.Key{}
	if final == '~' {
		switch params {
		case "1", "7":
			key.Special = Home
		case "4", "8":
			key.Special = End
		case "5":
			key.Special = keys.PageUp
		case "6":
			key.Special = keys.PageDown
		case "11":
			key.Special = keys.F1
		default:
			return key, false
		}
		return key, true
	}
	if params != "" && params != "1" {
		base, modifier, ok := strings.Cut(params, ";")
		if !ok || base != "1" {
			return key, false
		}
		mod, err := strconv.Atoi(modifier)
		if err != nil || mod < 2 || mod > 8 || (mod-1)&2 != 0 {
			return key, false
		}
		key.Ctrl = (mod-1)&4 != 0
	}
	switch final {
	case 'A':
		key.Special = keys.Up
	case 'B':
		key.Special = keys.Down
	case 'C':
		key.Special = keys.Right
	case 'D':
		key.Special = keys.Left
	case 'H':
		key.Special = Home
	case 'F':
		key.Special = End
	case 'P':
		key.Special = keys.F1
	default:
		return key, false
	}
	return key, true
}
