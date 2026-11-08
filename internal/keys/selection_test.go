package keys

import "testing"

func TestSelectionModeCommandArguments(t *testing.T) {
	for _, test := range []struct {
		line string
		mode string
	}{
		{":selection-mode", ""},
		{":selection-mode   ", ""},
		{"selection-mode text", "text"},
		{":selection-mode page", "page"},
		{" \t: selection-mode\tpage \t", "page"},
	} {
		t.Run(test.line, func(t *testing.T) {
			command, err := ParseCommandLine(test.line)
			want := Command{Action: SetSelectionMode, Text: test.mode}
			if err != nil || command != want {
				t.Fatalf("ParseCommandLine = %+v, %v; want %+v", command, err, want)
			}
		})
	}
	for _, argument := range []string{"garbage", "Page", "TEXT", "text page", "page extra", "%", "\"page\"", "1"} {
		t.Run("invalid "+argument, func(t *testing.T) {
			command, err := ParseCommandLine(":selection-mode " + argument)
			if err == nil || command != (Command{}) {
				t.Fatalf("invalid argument emitted %+v, %v", command, err)
			}
		})
	}
}

func TestInvalidSelectionModeCanBeCorrectedWithoutDispatch(t *testing.T) {
	var decoder Decoder
	for _, key := range ending(":selection-mode garbage", Enter) {
		if command, ok := decoder.Feed(key); ok {
			t.Fatalf("invalid mode dispatched %+v", command)
		}
	}
	if decoder.Mode() != CommandLine || decoder.Pending() != ":selection-mode garbage" {
		t.Fatalf("invalid mode lost editable input: mode %v, pending %q", decoder.Mode(), decoder.Pending())
	}
	if command, ok := decoder.Feed(Key{Rune: 'w', Ctrl: true}); ok {
		t.Fatalf("prompt editing dispatched %+v", command)
	}
	for _, key := range textKeys("page") {
		if command, ok := decoder.Feed(key); ok {
			t.Fatalf("unfinished correction dispatched %+v", command)
		}
	}
	command, ok := decoder.Feed(Key{Special: Enter})
	if !ok || command != (Command{Action: SetSelectionMode, Text: "page"}) {
		t.Fatalf("corrected mode = %+v, %v", command, ok)
	}
	if decoder.Mode() != Normal || decoder.Pending() != "" {
		t.Fatalf("corrected submission left mode %v, pending %q", decoder.Mode(), decoder.Pending())
	}
	if command, ok := decoder.Feed(Key{Rune: 'v'}); !ok || command != (Command{Action: Select}) {
		t.Fatalf("selection after correction = %+v, %v", command, ok)
	}
}

func TestSelectionDefaultAndDirectOverrides(t *testing.T) {
	var decoder Decoder
	for _, test := range []struct {
		input []Key
		want  Command
	}{
		{textKeys("v"), Command{Action: Select}},
		{ending(":select", Enter), Command{Action: Select}},
		{ending(":text", Enter), Command{Action: SelectText}},
		{ending(":page-selection", Enter), Command{Action: SelectPageText}},
		{ending(":selection-mode", Enter), Command{Action: SetSelectionMode}},
	} {
		for i, key := range test.input {
			command, ok := decoder.Feed(key)
			if i < len(test.input)-1 {
				if ok {
					t.Fatalf("unfinished selection input dispatched %+v", command)
				}
			} else if !ok || command != test.want {
				t.Fatalf("selection command = %+v, %v; want %+v", command, ok, test.want)
			}
		}
	}
	for _, line := range []string{":select page", ":text page", ":page-selection text", ":copy-page"} {
		if command, err := ParseCommandLine(line); err == nil || command != (Command{}) {
			t.Fatalf("unsupported override %q emitted %+v, %v", line, command, err)
		}
	}
}
