package main

import (
	"fmt"
	"os"

	"go.olrik.dev/overseer/cmd"
)

func main() {
	// If called as companion wrapper, inject "companion-run" argument
	if os.Getenv("OVERSEER_COMPANION_RUN_ALIAS") != "" {
		os.Args = []string{os.Args[0], "companion-run"}
	}

	// If called by SSH as askpass helper, inject "askpass" argument
	// SSH invokes SSH_ASKPASS without arguments
	if os.Getenv("OVERSEER_ASKPASS_ALIAS") != "" {
		os.Args = []string{os.Args[0], "askpass"}
	}

	// If no command specified, default to status
	if len(os.Args) == 1 {
		os.Args = []string{os.Args[0], "status"}
	}

	root := cmd.NewRootCommand()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
