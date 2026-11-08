package tui

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// inputStyles leaves a text input's prompt, text and cursor in the terminal's own
// foreground, where the library's defaults would paint them white.
func inputStyles() textinput.Styles {
	s := textinput.DefaultDarkStyles()
	s.Focused.Prompt = lipgloss.NewStyle()
	s.Blurred.Prompt = lipgloss.NewStyle()
	s.Blurred.Text = lipgloss.NewStyle()
	s.Cursor.Color = nil
	return s
}

// inputView draws a text input with its whole placeholder. With no width set,
// bubbles draws only a placeholder's first character, so this copy gets the width
// that draws the rest and adds no padding.
func inputView(input textinput.Model) string {
	if input.Value() == "" && input.Placeholder != "" {
		input.SetWidth(lipgloss.Width(input.Placeholder) - 1)
	}
	return input.View()
}

// paneBox is a pane's border at an outer width, around an inner height of rows.
func paneBox(active bool, width, innerHeight int) lipgloss.Style {
	border := inactiveBorderStyle
	if active {
		border = activeBorderStyle
	}
	return border.Width(width).Height(innerHeight + 2)
}

// overlayBox frames an overlay as wide as the window allows, held between lo and hi
// columns inside its border.
func overlayBox(content string, windowWidth, lo, hi int) string {
	inside := max(min(windowWidth-4, hi), lo)
	return overlayBoxStyle.Width(inside + 2).Render(content)
}

// All colors use ANSI 0-15 so the TUI adapts to the terminal's theme.
//
//	0=black  1=red     2=green   3=yellow  4=blue    5=magenta  6=cyan    7=white
//	8=bright black (dim)  9-15=bright variants of 1-7
//
// A style's Width and Height count its border, so a bordered box is sized two wider
// and two taller than the area inside its border.
var (
	// Pane borders
	activeBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.ThickBorder()).
				BorderForeground(lipgloss.Color("4"))

	inactiveBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("8"))

	// Project list
	projectNormalStyle   = lipgloss.NewStyle().PaddingLeft(1)
	projectSelectedStyle = lipgloss.NewStyle().
				PaddingLeft(0).
				Foreground(lipgloss.Color("2")).
				Bold(true)
	projectMultiSelectedStyle = lipgloss.NewStyle().
					PaddingLeft(1).
					Foreground(lipgloss.Color("5"))

	// Project item counts. Color carries the whole signal: a zero is dim
	// because "nothing done yet" and "nothing left open" are both states you
	// want to read off the pane without doing arithmetic on a total.
	projectOpenCountStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3"))
	projectDoneCountStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2"))

	// All pseudo-project
	allProjectStyle = lipgloss.NewStyle().
			PaddingLeft(0).
			Foreground(lipgloss.Color("6")).
			Bold(true)

	// Group header in All Items view
	groupHeaderStyle = lipgloss.NewStyle().
				PaddingLeft(1).
				Foreground(lipgloss.Color("6")).
				Bold(true)

	// Item list
	itemNormalStyle   = lipgloss.NewStyle().PaddingLeft(1)
	itemSelectedStyle = lipgloss.NewStyle().
				PaddingLeft(0).
				Foreground(lipgloss.Color("2")).
				Bold(true)
	itemIDStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))
	itemCompletedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Strikethrough(true)
	multiProjectStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3"))
	notesIndicatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6"))
	blockerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			PaddingLeft(5)
	blockerProjectStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")).
				Bold(true)

	// Status bar
	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			PaddingLeft(1).
			PaddingRight(1)
	modeLocalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")).
			Bold(true)
	modeAPIStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4")).
			Bold(true)
	syncOKStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")).
			Bold(true)
	syncingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Bold(true)
	syncPendingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")).
				Bold(true)
	syncErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Bold(true)

	// Titles
	paneTitleStyle = lipgloss.NewStyle().
			Bold(true).
			PaddingLeft(1)

	// Empty state
	emptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Italic(true).
			PaddingLeft(2)

	// Overlay
	overlayBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("4")).
			Padding(1, 2)

	overlayTitleStyle = lipgloss.NewStyle().
				Bold(true)

	// Reader
	readerTitleStyle = lipgloss.NewStyle().
				Bold(true)
	readerBlockerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	// Project picker
	pickerSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")).
				Bold(true)

	pickerNormalStyle = lipgloss.NewStyle()

	// Status flash
	statusMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	errorMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Bold(true)

	// Filter indicator
	filterIndicatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")).
				Bold(true)

	// Move mode
	moveIndicatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")).
				Bold(true)

	// Tasks
	taskNormalStyle = lipgloss.NewStyle().
			PaddingLeft(5)
	taskSelectedStyle = lipgloss.NewStyle().
				PaddingLeft(4).
				Foreground(lipgloss.Color("2")).
				Bold(true)
	taskCompletedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Strikethrough(true)

	// Search results
	searchResultSelectedStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("2")).
					Bold(true)
	searchResultNormalStyle = lipgloss.NewStyle()
)
