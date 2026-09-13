// Package seed installs the demo dataset described in the assignment: three
// windows with their own playlists, covering image, video and blank media.
package seed

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// defaultVideoBase points at this backend's own embedded asset endpoint. It is
// stored as a root-relative path, which the frontend resolves against its
// configured API URL - so the same seeded rows work in local development and in
// production without rewriting anything.
//
// Set SEED_VIDEO_BASE_URL to an absolute base (for example a CDN) to seed from
// your own media host instead.
const defaultVideoBase = "/api/assets"

// videoURL builds a seed video URL from the configured base.
func videoURL(file string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SEED_VIDEO_BASE_URL")), "/")
	if base == "" {
		base = defaultVideoBase
	}
	return base + "/" + file
}

// imageDataURI renders a labelled placeholder card as an inline SVG data URI.
//
// Seed images are generated rather than linked so the demo renders correctly
// with no third-party image host, no network flakiness and no broken links -
// exactly the kind of instability that makes a playback demo hard to evaluate.
// Real deployments add real URLs through the UI or POST /api/media.
func imageDataURI(label, caption, from, to string) string {
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1280 720">`+
		`<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">`+
		`<stop offset="0%%" stop-color="%s"/><stop offset="100%%" stop-color="%s"/>`+
		`</linearGradient></defs>`+
		`<rect width="1280" height="720" fill="url(#g)"/>`+
		`<circle cx="1080" cy="150" r="220" fill="#ffffff" opacity="0.08"/>`+
		`<circle cx="200" cy="620" r="260" fill="#ffffff" opacity="0.06"/>`+
		`<text x="640" y="330" font-family="Helvetica,Arial,sans-serif" font-size="210" `+
		`font-weight="700" fill="#ffffff" text-anchor="middle">%s</text>`+
		`<text x="640" y="430" font-family="Helvetica,Arial,sans-serif" font-size="52" `+
		`fill="#ffffff" opacity="0.85" text-anchor="middle">%s</text>`+
		`</svg>`, from, to, label, caption)

	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}
