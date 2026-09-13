// Package migrations embeds the SQL schema files into the binary so that a
// deployed container carries its own migrations and needs no external tooling.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
