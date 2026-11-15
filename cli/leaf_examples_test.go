package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestEveryLeafAReaderReachesCarriesAnExample reads each runnable leaf's
// Example field. A leaf's help has to stand on its own for someone who arrived
// by tab-completion, and an example that runs is the part they copy, so a leaf
// with only Short and Long leaves them guessing at its arguments.
//
// A hidden or deprecated command is listed on no help screen, so it owes no
// reader an example.
func TestEveryLeafAReaderReachesCarriesAnExample(t *testing.T) {
	leaves := 0
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, child := range cmd.Commands() {
			walk(child)
		}
		if !cmd.Runnable() || cmd.HasAvailableSubCommands() || hiddenFromHelp(cmd) {
			return
		}
		leaves++
		if cmd.Example == "" {
			t.Errorf("%s: runnable leaf has no Example", cmd.CommandPath())
		}
	}
	walk(NewRootCommand())
	// Without this, a walk that stops reaching the tree passes having read nothing.
	if leaves == 0 {
		t.Error("read no runnable leaf, so the walk checked nothing")
	}
}

// hiddenFromHelp reports whether no help screen lists cmd: it or a parent is
// hidden, or it is deprecated.
func hiddenFromHelp(cmd *cobra.Command) bool {
	for at := cmd; at != nil; at = at.Parent() {
		if at.Hidden || at.Deprecated != "" {
			return true
		}
	}
	return false
}
