package main

import (
	"fmt"
	"os"

	"github.com/datapointchris/projects/cli"
	"github.com/datapointchris/projects/config"
)

func main() {
	for _, refusal := range []string{config.RetiredVariableRefusal(), config.RetiredSyncVariableRefusal()} {
		if refusal != "" {
			fmt.Fprintf(os.Stderr, "error: %s\n", refusal)
			os.Exit(2)
		}
	}
	os.Exit(cli.Execute())
}
