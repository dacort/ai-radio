// cmd/serve.go is the entry point for the babble CLI. The //go:embed directive
// must live in this file (package cmd) so that the embed path "web" resolves
// relative to this file's directory, i.e. cmd/web/.
package cmd

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/dacort/babble/internal/server"
	"github.com/dacort/babble/internal/sessions"
)

//go:embed all:web
var webFS embed.FS

// Execute is the top-level entry point called from main. It parses the
// subcommand from os.Args and dispatches accordingly.
func Execute() error {
	serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
	port := serveCmd.Int("p", 3333, "port to listen on")
	noOpen := serveCmd.Bool("no-open", false, "don't auto-open browser")

	if len(os.Args) < 2 {
		fmt.Println("Usage: babble <command>")
		fmt.Println("  serve    Start the Babble server")
		return nil
	}

	switch os.Args[1] {
	case "serve":
		serveCmd.Parse(os.Args[2:])
		return runServe(*port, *noOpen)
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

// runServe builds and wires all components, then starts the HTTP server.
func runServe(port int, noOpen bool) error {
	home, _ := os.UserHomeDir()
	watchPath := filepath.Join(home, ".claude", "projects")

	staticFS, _ := fs.Sub(webFS, "web")

	srv := server.New(port, staticFS)

	mgr := sessions.NewManager(watchPath, srv.EventCh())
	go mgr.Start()

	if !noOpen {
		url := fmt.Sprintf("http://localhost:%d", port)
		openBrowser(url)
	}

	return srv.Start()
}

// openBrowser attempts to open url in the default system browser.
func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	case "linux":
		exec.Command("xdg-open", url).Start()
	}
}
