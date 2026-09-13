// Package assets embeds the demo media that ships with the application.
//
// The seed data deliberately does not link to third-party media hosts: sample
// video URLs rot, and a playback demo that silently shows a fallback card is
// impossible to evaluate. These clips are served by the backend itself at
// /api/assets/{name}, so the demo works offline, behind a firewall and in any
// deployment without configuration.
//
// See ATTRIBUTION.md for the licences of the bundled clips.
package assets

import "embed"

//go:embed *.mp4
var FS embed.FS
