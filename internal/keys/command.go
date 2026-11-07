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
	tabArgument
	newTabArgument
	moveArgument
)

type lineBinding struct {
	name        string
	helpKeys    string
	action      Action
	argument    lineArgument
	description string
}

var lineBindings = [...]lineBinding{
	{"e", "e {path|%}", OpenFile, pathArgument, "Open a PDF in a new tab; literal path, except % reopens the current file independently"},
	{"", "{N}", GoToPage, pageArgument, "Go to page N"},
	{"q", "q", CloseTab, noArgument, "Close the current tab; quit if it is the last"},
	{"qa", "qa", QuitAll, noArgument, "Quit all tabs"},
	{"tabnew", "tabnew [%]", NewTab, newTabArgument, "Open a new tab; :tabnew % reopens the current file independently"},
	{"tabclose", "tabclose", CloseTab, noArgument, "Close the current tab"},
	{"tab", "tab split", DuplicateTab, splitArgument, "Duplicate the current tab with its own view"},
	{"help", "help", HelpAction, noArgument, "Show key help"},
	{"outline", "outline", Outline, noArgument, "Open the document outline"},
	{"reload", "reload", Reload, noArgument, "Reload the file"},
	{"mark", "mark {a-z}", SetMark, markArgument, "Set a lowercase letter mark"},
	{"ls", "ls", TabList, noArgument, "List open tabs"},
	{"tabs", "tabs", TabList, noArgument, "List open tabs"},
	{"buffers", "buffers", TabList, noArgument, "List open tabs"},
	{"b", "b [N|name]", GoToTabByName, tabArgument, "Select tab N or a name fragment; no argument lists tabs"},
	{"tabmove", "tabmove {N|+N|-N}", MoveTab, moveArgument, "Move the tab to zero-based position N, or by signed displacement"},
	{"tabonly", "tabonly", CloseOtherTabs, noArgument, "Close all other tabs"},
	{"dark", "dark", ToggleDark, noArgument, "Toggle dark page colors"},
	{"sidebar", "sidebar", ToggleSidebar, noArgument, "Toggle the sidebar"},
}

// ParseCommandLine accepts a line with or without its initial colon. For :e,
// whitespace separating the command and path is skipped; the remaining path is
// kept exactly, including internal and trailing spaces. Quotes and backslashes
// are literal filename characters, not shell syntax; no expansion is performed.
// Thus :e my file.pdf opens "my file.pdf", not two arguments. A path beginning
// with whitespace must be written with a directory prefix, such as ./ my.pdf.
// A path exactly equal to % instead emits DuplicateTab with Text "%".
// MoveTab uses a zero-based absolute Count with Arg zero, or a signed relative
// Count with Arg '+' or '-'. For example :tabmove -2 uses Count -2 and Arg '-'.
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
			if argument == "%" {
				cmd.Action = DuplicateTab
			}
		case newTabArgument:
			argument = strings.TrimSpace(argument)
			if argument == "%" {
				cmd.Action, cmd.Text = DuplicateTab, "%"
			} else if argument != "" {
				return Command{}, fmt.Errorf("command %q accepts only %% or no argument", name)
			}
		case tabArgument:
			argument = strings.TrimSpace(argument)
			if argument == "" {
				cmd.Action = TabList
			} else if decimal(argument) {
				number, err := strconv.Atoi(argument)
				if err != nil || number < 1 {
					return Command{}, fmt.Errorf("invalid tab number %q: use a positive tab number", argument)
				}
				cmd.Action, cmd.Count = GoToTab, number
			} else {
				cmd.Text = argument
			}
		case moveArgument:
			argument = strings.TrimSpace(argument)
			digits := argument
			if len(digits) > 0 && (digits[0] == '+' || digits[0] == '-') {
				cmd.Arg = rune(digits[0])
				digits = digits[1:]
			}
			position, err := strconv.Atoi(argument)
			if !decimal(digits) || err != nil {
				return Command{}, fmt.Errorf("invalid tab position %q: use N, +N, or -N", argument)
			}
			cmd.Count = position
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
