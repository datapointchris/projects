package config

import (
	"fmt"
	"os"
)

// retiredVariable is an environment variable projects refuses rather than reads.
// Each chose where a run read or wrote, so ignoring it would send the run
// somewhere else.
type retiredVariable struct {
	name string
	// remedy follows "$<name> is set, " in the refusal.
	remedy string
}

// retiredVariables holds every retirement's rows. Each retirement adds its own
// from the file that holds it, so deleting that file takes its rows with it.
var retiredVariables []retiredVariable

// RetiredVariableRefusal names the first retired variable set to a value, or "" where none is.
func RetiredVariableRefusal() string {
	for _, variable := range retiredVariables {
		if os.Getenv(variable.name) != "" {
			return fmt.Sprintf("$%s is set, %s", variable.name, variable.remedy)
		}
	}
	return ""
}
