package httpapi

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// consoleAssets returns the filesystem holding the built Next.js export.
//
// It is a variable rather than a direct call so the two build variants (embedded, and
// "read from disk for `go run`") are interchangeable without the router knowing which
// one it got.
var consoleAssets = loadConsoleAssets()

// mountConsole serves the static console export under /console.
//
// The API routes and the OAuth callback are registered separately and are matched by
// more specific patterns, so this only has to answer for documents and assets. It must
// not swallow a path the API owns.
func mountConsole(root *http.ServeMux, assets fs.FS) {
	if assets == nil {
		return
	}
	fileServer := http.FileServer(http.FS(assets))

	handler := func(w http.ResponseWriter, r *http.Request) {
		// The export is mounted under /console, so strip the prefix before looking the
		// path up in the filesystem.
		rel := strings.TrimPrefix(r.URL.Path, "/console")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			rel = "index.html"
		}

		if _, err := fs.Stat(assets, rel); err != nil {
			// The app router emits a document per route. Falling back to the root
			// document keeps a deep link such as /console/upload working instead of
			// returning a 404 from the static server.
			if rel != "index.html" {
				if _, err := fs.Stat(assets, "index.html"); err == nil {
					serveConsoleFile(w, r, fileServer, "index.html")
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		serveConsoleFile(w, r, fileServer, rel)
	}

	root.Handle("GET /console/_next/", http.HandlerFunc(handler))
	root.Handle("GET /console/", http.HandlerFunc(handler))
}

func serveConsoleFile(w http.ResponseWriter, r *http.Request, fileServer http.Handler, name string) {
	// Hashed assets are immutable; the documents are not. A cached console would show a
	// stale control state to whoever loads it next.
	if strings.HasPrefix(name, "_next/static/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
	}
	http.StripPrefix("/console", fileServer).ServeHTTP(w, r)
}

// consoleFromDisk reads a console export from the filesystem, for `go run`.
//
// It is a convenience, not a production path: a shipped binary embeds the export, so the
// console cannot be missing because someone forgot to run a build step.
func consoleFromDisk() fs.FS {
	dir := os.Getenv("STORVIA_CONSOLE_DIR")
	if dir == "" {
		dir = filepath.Join("console", "out")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		return nil
	}
	return os.DirFS(dir)
}

// withConsoleAssets mounts a stand-in export for the duration of a test.
//
// It exists so the Go suite can exercise the console's routes without requiring a Node
// build. The production path embeds the real export; a test that could only pass after
// running `npm run build` would simply get skipped.
func withConsoleAssets(assets fs.FS, fn func(http.Handler)) {
	original := consoleAssets
	consoleAssets = assets
	defer func() { consoleAssets = original }()

	mux := http.NewServeMux()
	mountConsole(mux, assets)
	fn(mux)
}
