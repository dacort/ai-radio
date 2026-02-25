// cmd/serve.go is the entry point for the babble CLI. The //go:embed directives
// must live in this file (package cmd) so that the embed paths resolve
// relative to this file's directory, i.e. cmd/web/ and cmd/soundpacks/.
package cmd

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/dacort/babble/internal/server"
	"github.com/dacort/babble/internal/sessions"
)

//go:embed all:web
var webFS embed.FS

//go:embed all:soundpacks
var defaultPacksFS embed.FS

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
	packsDir := filepath.Join(home, ".config", "babble", "soundpacks")

	ensureDefaultPack(packsDir)

	staticFS, _ := fs.Sub(webFS, "web")

	srv := server.New(port, staticFS, packsDir)

	mgr := sessions.NewManager(watchPath, srv.EventCh())
	go mgr.Start()

	if !noOpen {
		url := fmt.Sprintf("http://localhost:%d", port)
		openBrowser(url)
	}

	return srv.Start()
}

// ensureDefaultPack copies the embedded default sound pack into
// packsDir/default/ if it does not already exist. Errors are logged but do
// not prevent the server from starting — a missing default pack is
// non-fatal.
func ensureDefaultPack(packsDir string) {
	destManifest := filepath.Join(packsDir, "default", "pack.json")
	if _, err := os.Stat(destManifest); err == nil {
		// Already present; nothing to do.
		return
	}

	destDir := filepath.Join(packsDir, "default")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Printf("soundpacks: create %s: %v", destDir, err)
		return
	}

	// Walk the embedded soundpacks/default/ tree and copy every file.
	srcRoot := "soundpacks/default"
	err := fs.WalkDir(defaultPacksFS, srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Compute the destination path relative to srcRoot.
		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil {
			return relErr
		}
		dest := filepath.Join(destDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		return copyEmbeddedFile(defaultPacksFS, path, dest)
	})
	if err != nil {
		log.Printf("soundpacks: extract default pack: %v", err)
	}
}

// copyEmbeddedFile copies a single file from an embed.FS to a destination
// path on disk.
func copyEmbeddedFile(fsys embed.FS, src, dest string) error {
	in, err := fsys.Open(src)
	if err != nil {
		return fmt.Errorf("open embedded %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dest, err)
	}
	return nil
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
