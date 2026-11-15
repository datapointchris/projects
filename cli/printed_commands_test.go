package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// notCommands are printed `projects ...` spans that name no command of this
// binary. `projects sync` is the command a release before the synced copy was
// retired carried, which the message refusing an unpushed queue points to.
// `projects sync improvements` is an example project name.
var notCommands = map[string]bool{
	"sync":              true,
	"sync improvements": true,
}

// A command printed for a person to run: in a code span, after the colon of a
// hint, inside $(...), or at the start of an indented Example line.
var printedInGo = regexp.MustCompile("(?:`|: |\\$\\(|(?m:^)[ \\t]+)projects")

// The same spans in the README, and every line of a shell fence.
var (
	printedInReadme = regexp.MustCompile("`projects")
	shellFence      = regexp.MustCompile("(?ms)^```(?:bash|sh)\n(.*?)\n```")
	fenceLine       = regexp.MustCompile("(?m)^projects")
)

type printedCommand struct {
	source string
	words  []string
}

// wordsAfter reads the subcommand words following "projects" at text[end:].
// It stops at a flag, an argument placeholder, a quote or a double space, and a
// word running into a `/` or a `.` is a path argument rather than a subcommand.
// ok is false where "projects" is part of a longer word, such as projects-api.
func wordsAfter(text string, end int) (words []string, ok bool) {
	if end < len(text) && !strings.ContainsRune(" `)\"'\n", rune(text[end])) {
		return nil, false
	}
	for end < len(text) && text[end] == ' ' {
		start := end + 1
		stop := start
		for stop < len(text) && (text[stop] >= 'a' && text[stop] <= 'z' || text[stop] == '-' ||
			stop > start && text[stop] >= '0' && text[stop] <= '9') {
			stop++
		}
		if stop == start || text[start] == '-' {
			break
		}
		if stop < len(text) && (text[stop] == '/' || text[stop] == '.' && stop+1 < len(text) && text[stop+1] != ' ') {
			break
		}
		words = append(words, text[start:stop])
		end = stop
	}
	return words, true
}

func collect(source, text string, re *regexp.Regexp, into *[]printedCommand) {
	for _, match := range re.FindAllStringIndex(text, -1) {
		if words, ok := wordsAfter(text, match[1]); ok {
			*into = append(*into, printedCommand{source, words})
		}
	}
}

// printedCommands gathers every command the Go source prints in a string
// literal, and every one the README shows.
func printedCommands(t *testing.T) []printedCommand {
	t.Helper()
	var printed []printedCommand
	files := token.NewFileSet()
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "web", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(files, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			// A Short is the one-line summary a help list shows, and its prose
			// reads "the board: projects and their items" with no command in it.
			if field, isField := node.(*ast.KeyValueExpr); isField {
				if key, isIdent := field.Key.(*ast.Ident); isIdent && key.Name == "Short" {
					return false
				}
			}
			literal, isLiteral := node.(*ast.BasicLit)
			if !isLiteral || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Errorf("%s: %v", files.Position(literal.Pos()), err)
				return true
			}
			collect(files.Position(literal.Pos()).String(), value, printedInGo, &printed)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	collect("README.md", string(readme), printedInReadme, &printed)
	for _, fence := range shellFence.FindAllStringSubmatch(string(readme), -1) {
		collect("README.md", fence[1], fenceLine, &printed)
	}
	return printed
}

// subcommand finds the child of cmd answering to name, by name or alias.
func subcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == name || child.HasAlias(name) {
			return child
		}
	}
	return nil
}

// TestEveryCommandItPrintsIsInTheCommandTree resolves each printed command
// against the assembled tree, word by word, until it reaches a command with no
// subcommands. A hint naming a command that was renamed or removed sends its
// reader to an error, and no help screen shows the mistake.
func TestEveryCommandItPrintsIsInTheCommandTree(t *testing.T) {
	root := NewRootCommand()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	printed := printedCommands(t)
	// Without this, a pattern that stops matching passes having read nothing.
	if len(printed) < 20 {
		t.Fatalf("found %d printed commands, so the scan is missing the source", len(printed))
	}
	for _, command := range printed {
		if notCommands[strings.Join(command.words, " ")] {
			continue
		}
		at := root
		for _, word := range command.words {
			if !at.HasSubCommands() {
				break
			}
			next := subcommand(at, word)
			if next == nil {
				t.Errorf("%s: projects %s: %q is not a command under %q",
					command.source, strings.Join(command.words, " "), word, at.CommandPath())
				break
			}
			at = next
		}
	}
}
