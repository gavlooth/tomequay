package keys

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type lineArgument int

const (
	noArgument lineArgument = iota
	pathArgument
	markArgument
	splitArgument
	pageArgument
)

type lineBinding struct {
	name        string
	helpKeys    string
	action      Action
	argument    lineArgument
	description string
}

var lineBindings = [...]lineBinding{
	{"e", "e {path}", OpenFile, pathArgument, "Open a PDF in a new tab; path is literal, without quotes or escapes"},
	{"", "{N}", GoToPage, pageArgument, "Go to page N"},
	{"q", "q", CloseTab, noArgument, "Close the current tab; quit if it is the last"},
	{"qa", "qa", QuitAll, noArgument, "Quit all tabs"},
	{"tabnew", "tabnew", NewTab, noArgument, "Open a new tab"},
	{"tabclose", "tabclose", CloseTab, noArgument, "Close the current tab"},
	{"tab", "tab split", DuplicateTab, splitArgument, "Duplicate the current tab with its own view"},
	{"help", "help", HelpAction, noArgument, "Show key help"},
	{"outline", "outline", Outline, noArgument, "Open the document outline"},
	{"reload", "reload", Reload, noArgument, "Reload the file"},
	{"mark", "mark {a-z}", SetMark, markArgument, "Set a lowercase letter mark"},
}

// ParseCommandLine accepts a line with or without its initial colon. For :e,
// whitespace separating the command and path is skipped; the remaining path is
// kept exactly, including internal and trailing spaces. Quotes and backslashes
// are literal filename characters, not shell syntax; no expansion is performed.
// Thus :e my file.pdf opens "my file.pdf", not two arguments. A path beginning
// with whitespace must be written with a directory prefix, such as ./ my.pdf.
func ParseCommandLine(line string) (Command, error) {
	line = strings.TrimLeftFunc(line, unicode.IsSpace)
	line = strings.TrimPrefix(line, ":")
	line = strings.TrimLeftFunc(line, unicode.IsSpace)
	if line == "" {
		return Command{}, fmt.Errorf("empty command line")
	}
	name, argument := line, ""
	if i := strings.IndexFunc(line, unicode.IsSpace); i >= 0 {
		name = line[:i]
		argument = strings.TrimLeftFunc(line[i:], unicode.IsSpace)
	}
	for _, b := range lineBindings {
		if b.name != name && !(b.argument == pageArgument && decimal(name)) {
			continue
		}
		cmd := Command{Action: b.action}
		switch b.argument {
		case noArgument:
			if strings.TrimSpace(argument) != "" {
				return Command{}, fmt.Errorf("command %q takes no arguments", name)
			}
		case pathArgument:
			if argument == "" {
				return Command{}, fmt.Errorf("command %q needs a path", name)
			}
			cmd.Text = argument
		case markArgument:
			argument = strings.TrimSpace(argument)
			if len(argument) != 1 || argument[0] < 'a' || argument[0] > 'z' {
				return Command{}, fmt.Errorf("command %q needs one lowercase mark letter", name)
			}
			cmd.Arg = rune(argument[0])
		case splitArgument:
			if strings.TrimSpace(argument) != "split" {
				return Command{}, fmt.Errorf("unknown tab command %q; use :tab split", argument)
			}
		case pageArgument:
			page, err := strconv.Atoi(name)
			if err != nil || page < 1 || strings.TrimSpace(argument) != "" {
				return Command{}, fmt.Errorf("invalid page command %q: use a positive page number", line)
			}
			cmd.Count = page
		}
		return cmd, nil
	}
	return Command{}, fmt.Errorf("unknown command %q", name)
}

func decimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
