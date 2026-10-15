//go:build !consoleassets

package httpapi

import "io/fs"

// loadConsoleAssets returns the on-disk export, if one happens to be present.
//
// Without the consoleassets tag the binary falls back to reading console/out, which is
// what `go run` wants during development. A production build is expected to be made
// with the tag so the console is embedded.
func loadConsoleAssets() fs.FS {
	return consoleFromDisk()
}
