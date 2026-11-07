package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/projects/apiclient"
	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/prompt"
	"github.com/datapointchris/projects/repos"
)

func newItemsCreateCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "create [flags]",
		Short: "Create an item in one or more projects",
		Long: "Pass every field as a flag to create in one shot. Leave --title or --project\n" +
			"out at a terminal and the rest are asked for one at a time. Tab prints the\n" +
			"projects and repos, and typing part of a name first narrows them.\n" +
			"\n" +
			"Answer Project with + to make a new one without leaving the item. It asks\n" +
			"for a name, a description and a kind, then goes on filing.\n" +
			"\n" +
			"Project repeats until Enter on an empty one, so an item can join several.\n" +
			"Notes take as many lines as you want and end on a blank one. Ctrl-C abandons\n" +
			"the item.",
		Example: "  projects items create\n" +
			"  projects items create --project \"Kitchen remodel\" --title \"Get three quotes\"\n" +
			"  projects items create --project \"Kitchen remodel\" --title \"Pick tile\" --notes \"$(cat tile.md)\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			answers := flagAnswers(cmd, "title", "project", "repo", "notes")
			missing := missingFlags(answers, "title", "project")
			// Ahead of opening anything, so a caller that cannot be asked is told
			// which flag it left out.
			if len(missing) > 0 && !interactive(cmd) {
				return goclikit.UsageError(fmt.Errorf("%s required — pass them, or run from a terminal to be asked",
					strings.Join(missing, " and ")))
			}
			source, err := openForWriting(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = source.Close() }()
			ctx := cmd.Context()
			made := &createdProjects{}
			fields, err := itemCreateFields(ctx, source, made)
			if err != nil {
				return source.explain(err)
			}
			if err := validateAnswers(answers, fields); err != nil {
				return goclikit.UsageError(err)
			}
			if len(missing) > 0 {
				asked, err := runForm(cmd, prompt.Form{
					Intro:  "Creating an item. Ctrl-C to abandon it.",
					Fields: unanswered(fields, answers),
				})
				if errors.Is(err, errAborted) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "\nAborted.")
					made.report(cmd.ErrOrStderr())
					return nil
				}
				if err != nil {
					made.report(cmd.ErrOrStderr())
					return err
				}
				answers.Merge(asked)
			}
			input := apiclient.NewItem{Title: answers.Get("title"), ProjectIDs: answers.All("project")}
			if answers.Has("notes") {
				notes := answers.Get("notes")
				input.Notes = &notes
			}
			if answers.Has("repo") {
				repo := answers.Get("repo")
				input.Repo = &repo
			}
			item, err := source.client.CreateItem(ctx, input)
			if err != nil {
				made.report(cmd.ErrOrStderr())
				return source.explain(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), item)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created item %q (%s)\n", item.Title, handle(item.Number, item.ID))
			return nil
		},
	}
	cmd.Flags().String("title", "", "Item title (asked for when omitted)")
	cmd.Flags().String("notes", "", "Markdown notes for the item")
	cmd.Flags().String("repo", "", "Repo this item is work on, by registry name (optional)")
	cmd.Flags().StringArray("project", nil, "Project name or id to add the item to (repeatable; asked for when omitted)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the created item as JSON to stdout")
	return cmd
}

// itemCreateFields is the record `items create` builds, in the order it asks. The
// same list validates the flags, so a bad value reads the same through either
// door. The choices are read rather than declared, because projects and the repo
// registry change without this file changing.
func itemCreateFields(ctx context.Context, source *dataSource, made *createdProjects) ([]prompt.Field, error) {
	projects, err := source.client.ListProjects(ctx, apiclient.ProjectQuery{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(projects))
	for _, project := range projects {
		names = append(names, project.Name)
	}
	sort.Strings(names)
	registry, err := repos.Load(repos.DefaultPath())
	if err != nil {
		return nil, err
	}
	return []prompt.Field{
		{Key: "title", Label: "Title"},
		{
			Key:      "project",
			Label:    "Project",
			Hint:     "The project this belongs to. A repo is the Repo tag below, not a project.",
			Choices:  names,
			Repeat:   true,
			Validate: projectRef(names),
			Escape: &prompt.Escape{
				Trigger: newProjectTrigger,
				Hint:    newProjectTrigger + " makes a new project.",
				Run: func(session prompt.Session) (string, error) {
					name, err := createProjectFromForm(ctx, source, session)
					if name != "" {
						made.add(name)
					}
					return name, err
				},
			},
		},
		{
			Key:      "repo",
			Label:    "Repo",
			Hint:     "The repo this is work on. Leave it empty for work that is not code.",
			Choices:  registry.Names(),
			Optional: true,
			Validate: repoName(registry),
		},
		{Key: "notes", Label: "Notes", Optional: true, Multiline: true},
	}, nil
}

// newProjectTrigger is the answer to Project that makes one instead of picking
// one. No project is called "+".
const newProjectTrigger = "+"

// createdProjects is what the Project field's escape made during a form. A
// project is written the moment it is made, and the item can still be abandoned
// after, so the command names each one it leaves behind.
type createdProjects struct{ names []string }

func (c *createdProjects) add(name string) { c.names = append(c.names, name) }

func (c *createdProjects) report(out io.Writer) {
	for _, name := range c.names {
		_, _ = fmt.Fprintf(out, "Project %q was created and kept.\n", name)
	}
}

// createProjectFromForm makes a project without leaving the item being filed,
// and returns its name for the field that ran it. An empty name backs out and
// makes nothing. A refused create says why and asks again from the name, keeping
// the description and kind already given, because the name is what a refusal
// most often names.
func createProjectFromForm(ctx context.Context, source *dataSource, session prompt.Session) (string, error) {
	input := apiclient.NewProject{}
	for {
		name, err := askProjectName(session)
		if name == "" || err != nil {
			return "", err
		}
		input.Name = name
		answers, err := prompt.Form{Fields: newProjectFields(input)}.Run(session)
		if err != nil {
			return "", err
		}
		if description := answers.Get("description"); description != "" {
			input.Description = &description
		}
		if kind := answers.Get("kind"); kind != "" {
			input.Kind = &kind
		}
		project, err := source.client.CreateProject(ctx, input)
		if err == nil {
			_, _ = fmt.Fprintf(session, "  Created project %q.\n", project.Name)
			return project.Name, nil
		}
		_, _ = fmt.Fprintf(session, "  %v\n", source.explain(err))
	}
}

// askProjectName asks for the name alone, so an empty one backs out before the
// description and kind are asked for a project that will not exist.
func askProjectName(session prompt.Session) (string, error) {
	answers, err := prompt.Form{
		Intro:  "New project. Enter on an empty name goes back to picking one.",
		Fields: []prompt.Field{{Key: "name", Label: "Name", Optional: true}},
	}.Run(session)
	if err != nil {
		return "", err
	}
	if answers.Get("name") == "" {
		_, _ = fmt.Fprintln(session, "  No project made.")
		return "", nil
	}
	return answers.Get("name"), nil
}

// newProjectFields is the rest of the record, with what an earlier attempt
// collected as the defaults, so a retry is Enter twice.
func newProjectFields(input apiclient.NewProject) []prompt.Field {
	kind := model.KindLife
	if input.Kind != nil {
		kind = *input.Kind
	}
	description := ""
	if input.Description != nil {
		description = *input.Description
	}
	return []prompt.Field{
		{
			Key:      "description",
			Label:    "Description",
			Hint:     "What the effort covers. A project without one collects the wrong items later.",
			Default:  description,
			Optional: true,
		},
		{
			Key:      "kind",
			Label:    "Kind",
			Default:  kind,
			Choices:  model.ProjectKinds,
			Validate: prompt.OneOf(model.ProjectKinds),
		},
	}
}

// projectRef accepts a project by name, case-insensitively, and returns the name
// as the API spells it. A UUID passes straight through, because only names can be
// offered as choices.
func projectRef(names []string) func(string) (string, error) {
	byName := prompt.OneOf(names)
	return func(answer string) (string, error) {
		if looksLikeUUID(answer) {
			return answer, nil
		}
		return byName(answer)
	}
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// repoName accepts a registered repo name, and anything where the registry is
// missing, as --repo does.
func repoName(registry *repos.Registry) func(string) (string, error) {
	return func(answer string) (string, error) {
		if err := registry.Validate(answer); err != nil {
			return "", err
		}
		return answer, nil
	}
}
