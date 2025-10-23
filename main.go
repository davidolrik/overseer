package main

import (
	"fmt"
	"os"

	"olrik.dev/davidolrik/overseer/cmd"
)

func main() {
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
