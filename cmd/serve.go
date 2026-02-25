// cmd/serve.go
package cmd

import (
	"flag"
	"fmt"
	"os"
)

func Execute() error {
	serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
	port := serveCmd.Int("p", 3333, "port to listen on")

	if len(os.Args) < 2 {
		fmt.Println("Usage: babble <command>")
		fmt.Println("  serve    Start the Babble server")
		return nil
	}

	switch os.Args[1] {
	case "serve":
		serveCmd.Parse(os.Args[2:])
		return runServe(*port)
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func runServe(port int) error {
	fmt.Printf("Babble listening on http://localhost:%d\n", port)
	return nil
}
