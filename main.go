// main.go
package main

import (
	"fmt"
	"os"

	"github.com/dacort/babble/cmd"
)

const version = "v0.1.0"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-version" {
		fmt.Println("babble " + version)
		os.Exit(0)
	}

	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
