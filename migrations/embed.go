// Package migrations embeds the versioned SQL schema files.
//
// The SQL lives at the repository root under migrations/, matching the layout in
// docs/architecture.md. This package exists only to make those files embeddable,
// so a compiled binary can migrate without shipping loose files beside it.
//
// Each dialect has its own directory. The schemas are deliberately not shared:
// the job-lease claim query, identifier types, and boolean handling differ
// between SQLite and PostgreSQL, and a single pretend-portable file would pass in
// development and fail in production.
package migrations

import "embed"

//go:embed sql
var files embed.FS

// FS returns the embedded migration tree, rooted so that "sql/<dialect>/" is
// reachable from it.
func FS() embed.FS { return files }
