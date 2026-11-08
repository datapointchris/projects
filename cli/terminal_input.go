package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/datapointchris/projects/prompt"
)

// confirm asks a yes/no question on stderr and reads the answer from stdin. With
// no terminal it refuses rather than read an answer nobody typed.
func confirm(cmd *cobra.Command, question string) (bool, error) {
	if !interactive(cmd) {
		return false, errors.New("refusing to prompt without an interactive terminal; pass --yes to confirm")
	}
	return readConfirmation(cmd.ErrOrStderr(), cmd.InOrStdin(), question)
}

// interactive is whether a person can answer: stdin is a terminal and --no-input
// was not passed. A command that prompts a caller which cannot answer blocks on a
// stdin that never closes.
func interactive(cmd *cobra.Command) bool {
	if noInput {
		return false
	}
	file, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func readConfirmation(out io.Writer, in io.Reader, question string) (bool, error) {
	_, _ = fmt.Fprintf(out, "%s [y/N] ", question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// errAborted is a form the person canceled. Abandoning a record half typed is a
// choice, so the command reports it and exits 0.
var errAborted = errors.New("aborted")

// runForm asks the fields the flags left out and returns what was answered. Call
// it only behind interactive(cmd).
func runForm(cmd *cobra.Command, form prompt.Form) (prompt.Answers, error) {
	file, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return nil, errors.New("stdin is not a terminal")
	}
	session, err := prompt.NewTerminalSession(file, cmd.ErrOrStderr())
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	answers, err := form.Run(session)
	if errors.Is(err, io.EOF) {
		return nil, errAborted
	}
	return answers, err
}

// flagAnswers collects the named flags the caller passed, in the shape a form
// returns, so nothing downstream asks which door a field came in through. A flag
// set to "" counts as unset.
func flagAnswers(cmd *cobra.Command, keys ...string) prompt.Answers {
	answers := prompt.Answers{}
	for _, key := range keys {
		flag := cmd.Flags().Lookup(key)
		if flag == nil || !flag.Changed {
			continue
		}
		if values := flagValues(flag); len(values) > 0 {
			answers[key] = values
		}
	}
	return answers
}

func flagValues(flag *pflag.Flag) []string {
	raw := []string{flag.Value.String()}
	if slice, ok := flag.Value.(pflag.SliceValue); ok {
		raw = slice.GetSlice()
	}
	var values []string
	for _, value := range raw {
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

// unanswered is the fields nothing has answered yet, which is the form to ask.
func unanswered(fields []prompt.Field, answers prompt.Answers) []prompt.Field {
	var ask []prompt.Field
	for _, field := range fields {
		if !answers.Has(field.Key) {
			ask = append(ask, field)
		}
	}
	return ask
}

// missingFlags is the flag spelling of each required key nothing answered, so a
// caller with no terminal is told which flag to pass.
func missingFlags(answers prompt.Answers, keys ...string) []string {
	var missing []string
	for _, key := range keys {
		if !answers.Has(key) {
			missing = append(missing, "--"+key)
		}
	}
	return missing
}

// validateAnswers runs each field's Validate over every value that answered it,
// so a bad value reads the same through a flag as through the form.
func validateAnswers(answers prompt.Answers, fields []prompt.Field) error {
	for _, field := range fields {
		if field.Validate == nil || !answers.Has(field.Key) {
			continue
		}
		values := append([]string(nil), answers.All(field.Key)...)
		for i, value := range values {
			canonical, err := field.Validate(value)
			if err != nil {
				return fmt.Errorf("--%s: %w", field.Key, err)
			}
			values[i] = canonical
		}
		answers[field.Key] = values
	}
	return nil
}
