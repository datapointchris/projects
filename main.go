package main

import (
	"fmt"
	"os"

	"github.com/datapointchris/projects/cli"
	"github.com/datapointchris/projects/config"
)

func main() {
	if refusal := config.RetiredVariableRefusal(); refusal != "" {
		fmt.Fprintf(os.Stderr, "error: %s\n", refusal)
		os.Exit(2)
	}
	os.Exit(cli.Execute())
}
