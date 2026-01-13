package main

import (
	"fmt"
	"os"

	"overseer.olrik.dev/cmd"
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

	root := cmd.NewRootCommand()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
