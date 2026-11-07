package cli

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/projects/model"
)

// itemStatuses is what an item --status takes. They partition every item, because
// archived beats completed.
var itemStatuses = []string{model.ItemStatusOpen, model.ItemStatusCompleted, model.ItemStatusArchived, model.StatusAll}

// projectStatuses is what a project --status takes.
var projectStatuses = append(slices.Clone(model.ProjectStatuses), model.StatusAll)

// limitValue parses --limit and refuses a negative. pflag has no minimum, and a
// -1 reaching a slice expression panics where a usage error was the answer.
type limitValue struct{ target *int }

func (v limitValue) String() string { return strconv.Itoa(*v.target) }

func (v limitValue) Type() string { return "int" }

func (v limitValue) Set(raw string) error {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("invalid limit %q: a row count is a whole number", raw)
	}
	if n < 0 {
		return fmt.Errorf("invalid limit %d: a row count is zero or more", n)
	}
	*v.target = n
	return nil
}

// addLimitFlag declares --limit/-n on a list the API caps. Unset sends nothing, so
// the read is every row, and --limit 0 asks for none.
func addLimitFlag(cmd *cobra.Command, target *int) {
	*target = 0
	cmd.Flags().VarP(limitValue{target: target}, "limit", "n", "Maximum number of rows to return (default: every row)")
}

// addCapFlag declares --limit/-n on a command that caps what it prints rather than
// what it asks for.
func addCapFlag(cmd *cobra.Command, target *int, fallback int, what string) {
	*target = fallback
	cmd.Flags().VarP(limitValue{target: target}, "limit", "n", what)
}

// limitFlag is --limit, or nil when it was not set.
func limitFlag(cmd *cobra.Command) *int {
	if !cmd.Flags().Changed("limit") {
		return nil
	}
	found := *cmd.Flags().Lookup("limit").Value.(limitValue).target
	return &found
}

// capRows keeps the first limit rows, and none for a negative.
func capRows[T any](rows []T, limit int) []T {
	if limit < 0 || len(rows) == 0 {
		return []T{}
	}
	return rows[:min(limit, len(rows))]
}

// noteCappedRows says on stderr when a cap cut rows from a table, so a short table
// is never read as the whole of what there is.
func noteCappedRows(cmd *cobra.Command, shown, total int) {
	if shown == 0 || shown >= total {
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n%d of %d shown: -n %d shows the rest\n", shown, total, total)
}

// validateItemStatus refuses an unknown --status before the request, naming the
// words that work.
func validateItemStatus(cmd *cobra.Command, status string) error {
	return validateStatus(cmd, status, itemStatuses)
}

func validateStatus(cmd *cobra.Command, status string, known []string) error {
	if !cmd.Flags().Changed("status") || slices.Contains(known, status) {
		return nil
	}
	return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", status, strings.Join(known, ", ")))
}

// repoFlagValue validates --repo and renders it as a filter. Absent is nil, no
// filter. Present and empty asks for the untagged items, the work that is not
// code, which collapsing the two would leave unreachable.
func repoFlagValue(cmd *cobra.Command, source *dataSource, repo string) (*string, error) {
	if !cmd.Flags().Changed("repo") {
		return nil, nil
	}
	if err := validateRepoFlag(cmd, source, repo); err != nil {
		return nil, err
	}
	return &repo, nil
}

// validateRepoFlag refuses a --repo the registry does not know. On a write a
// mistyped tag drops the item out of every repo view, and on a read it selects
// nothing, which reads the same as a repo with no work.
func validateRepoFlag(cmd *cobra.Command, source *dataSource, repo string) error {
	if !cmd.Flags().Changed("repo") {
		return nil
	}
	registry, err := source.repoRegistry()
	if err != nil {
		return err
	}
	if err := registry.Validate(repo); err != nil {
		return goclikit.UsageError(err)
	}
	return nil
}

// shellQuote quotes a value a hint prints to be typed. Most project names carry
// spaces, so an unquoted one pastes back as several arguments.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hintHiddenItems says on stderr that the default hid completed and archived
// items, quoting back the command that was run with --status all. Silent when a
// status was asked for, because then nothing was hidden the caller did not choose.
func hintHiddenItems(cmd *cobra.Command, asJSON bool) {
	if asJSON || cmd.Flags().Changed("status") {
		return
	}
	widen := cmd.CommandPath()
	for _, arg := range cmd.Flags().Args() {
		widen += " " + shellQuote(arg)
	}
	if project, err := cmd.Flags().GetString("project"); err == nil && project != "" {
		widen += " --project " + shellQuote(project)
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nCompleted and archived items are hidden: %s --status all\n", widen)
}

// argumentBelongsTo refuses a positional argument on a command that takes its
// subject as a flag, naming the flag.
func argumentBelongsTo(flag, purpose string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		refusal := fmt.Sprintf("%s takes no arguments, and was given %q\n\n%s: %s %s %s",
			cmd.CommandPath(), args[0], purpose, cmd.CommandPath(), flag, shellQuote(args[0]))
		return goclikit.UsageError(errors.New(refusal))
	}
}

// windowForms is what --since and --until each take.
const windowForms = "a span back from now (30m, 12h, 7d, 2w), a day (2026-10-01) or an RFC 3339 instant"

const dayLayout = "2006-01-02"

var (
	windowSpan      = regexp.MustCompile(`^(\d+)([smhdw])$`)
	windowMonthSpan = regexp.MustCompile(`^\d+(mo|M|y|Y)$`)
)

func addWindowFlags(cmd *cobra.Command, rows string) {
	cmd.Flags().String("since", "", fmt.Sprintf("Only %s at or after this: %s", rows, windowForms))
	cmd.Flags().String("until", "", fmt.Sprintf("Only %s before this; a day includes all of it", rows))
}

// readWindow resolves --since and --until, reading a day in this machine's zone.
func readWindow(cmd *cobra.Command) (since, until *time.Time, err error) {
	sinceFlag, _ := cmd.Flags().GetString("since")
	untilFlag, _ := cmd.Flags().GetString("until")
	now := time.Now()
	if sinceFlag != "" {
		bound, err := resolveBound(sinceFlag, false, now, time.Local)
		if err != nil {
			return nil, nil, goclikit.UsageError(fmt.Errorf("--since %q: %w", sinceFlag, err))
		}
		since = &bound
	}
	if untilFlag != "" {
		bound, err := resolveBound(untilFlag, true, now, time.Local)
		if err != nil {
			return nil, nil, goclikit.UsageError(fmt.Errorf("--until %q: %w", untilFlag, err))
		}
		until = &bound
	}
	return since, until, nil
}

// resolveBound reads one bound as an instant. Days and weeks step the calendar,
// and a day is its first instant on --since and the next day's on --until, so the
// whole day is inside the window.
func resolveBound(value string, until bool, now time.Time, loc *time.Location) (time.Time, error) {
	if m := windowSpan.FindStringSubmatch(value); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, err
		}
		switch m[2] {
		case "s":
			return now.Add(-time.Duration(n) * time.Second), nil
		case "m":
			return now.Add(-time.Duration(n) * time.Minute), nil
		case "h":
			return now.Add(-time.Duration(n) * time.Hour), nil
		case "d":
			return now.AddDate(0, 0, -n), nil
		default:
			return now.AddDate(0, 0, -7*n), nil
		}
	}
	if windowMonthSpan.MatchString(value) {
		return time.Time{}, errors.New("a month or a year has no fixed length; write weeks (26w) or a day")
	}
	if day, err := time.ParseInLocation(dayLayout, value, loc); err == nil {
		if until {
			return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc), nil
		}
		return day, nil
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at, nil
	}
	return time.Time{}, fmt.Errorf("want %s", windowForms)
}
