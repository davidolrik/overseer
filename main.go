package main

import (
	"fmt"
	"os"

	"github.com/jedib0t/go-pretty/text"
	"olrik.dev/davidolrik/overseer/cmd"
)


func main() {
	text.EnableColors()

	root := cmd.NewRootCommand()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}