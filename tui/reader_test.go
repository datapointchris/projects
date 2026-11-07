package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/datapointchris/projects/model"
)

// longNote is about ten thousand characters of the markdown a real note holds.
// Each paragraph is one source line several rows wide, which is the input where
// counting newlines and counting rows disagree.
func longNote() string {
	para := strings.Repeat("A paragraph long enough to wrap several times at any reader width. ", 6)
	var b strings.Builder
	for i := range 20 {
		fmt.Fprintf(&b, "## Section %d\n\n%s\n\n"+
			"\tif err := run(ctx, \"a command\"); err != nil {\n\t\treturn fmt.Errorf(\"run the command with its arguments, its flags and its environment: %%w\", err)\n\t}\n\n"+
			"| key | value |\n|---|---|\n| one | **two** |\n\n- first\n- second\n\n", i, para)
	}
	return b.String()
}

// screenWidth is the columns a terminal draws row in. A tab advances to the
// next multiple of eight, where lipgloss.Width counts it as nothing.
func screenWidth(row string) int {
	col := 0
	for i, part := range strings.Split(ansi.Strip(row), "\t") {
		if i > 0 {
			col += 8 - col%8
		}
		col += lipgloss.Width(part)
	}
	return col
}

// A long note opens at its top, scrolls by the rows it renders into, and the
// view never outgrows the window, so the title stays on screen. The
// list row is where esc lands, and an action key acts on the item shown.
func TestReaderScrollsALongNoteByRenderedRows(t *testing.T) {
	note := longNote()
	app := newTestAppWithItems(t, 80, 24, []model.CreateProjectItem{
		{Title: "first"},
		{Title: "long note", Notes: &note},
	})
	pressKey(t, app, "j")
	row := app.rowCursor

	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.appMode != modeReader || app.itemDetail == nil || app.itemDetail.Title != "long note" {
		t.Fatalf("enter did not open the reader on the item, mode %v", app.appMode)
	}
	if app.readerScroll != 0 {
		t.Errorf("the reader opened at row %d, want the top", app.readerScroll)
	}

	width := app.readerWidth()
	rows := app.readerRows(width)
	for i, r := range rows {
		if got := screenWidth(r); got > width {
			t.Fatalf("row %d is %d wide at width %d, so it is more than one screen row", i, got, width)
		}
	}

	pressKey(t, app, "G")
	if want := len(rows) - app.readerHeight(); app.readerScroll != want {
		t.Errorf("G scrolled to row %d of %d, want %d so the last row sits at the bottom", app.readerScroll, len(rows), want)
	}
	if got := lipgloss.Height(app.screen()); got > app.height {
		t.Errorf("the reader is %d lines in a %d-line window", got, app.height)
	}
	send(t, app, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if want := len(rows) - app.readerHeight() - app.readerHeight()/2; app.readerScroll != want {
		t.Errorf("ctrl+u scrolled to row %d, want %d", app.readerScroll, want)
	}

	pressKey(t, app, " ")
	item, err := app.backend.GetItem(app.itemDetail.ID)
	if err != nil {
		t.Fatalf("reading the item back: %v", err)
	}
	if !item.Completed {
		t.Error("space in the reader did not mark the item shown done")
	}
	if app.appMode != modeReader || !app.itemDetail.Completed {
		t.Errorf("the reader did not stay open on the updated item, mode %v", app.appMode)
	}
	pressKey(t, app, "u")
	if app.appMode != modeReader || app.itemDetail.Completed {
		t.Error("u in the reader undid the change but the reader still shows the item done")
	}

	send(t, app, tea.KeyPressMsg{Code: tea.KeyEscape})
	if app.appMode != modeNormal || app.rowCursor != row {
		t.Errorf("esc left mode %v on row %d, want the list on row %d", app.appMode, app.rowCursor, row)
	}
}

// A note's placeholder written as <slug> is text the reader shows, where glamour
// alone strips it as an HTML tag. Code spans and autolinks still render as code and links.
func TestReaderShowsAnAngleBracketedWordAsWritten(t *testing.T) {
	note := "Run `new <x>` then <slug>, and swap <old> for </new>.\n\n<details>\nsee <https://example.com>\n"
	got := ansi.Strip(strings.Join(renderMarkdown(note, 80), "\n"))
	for _, want := range []string{"new <x>", "<slug>", "<old>", "</new>", "<details>", "https://example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("the rendered note lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `\`) {
		t.Errorf("the rendered note shows an escape:\n%s", got)
	}
}
