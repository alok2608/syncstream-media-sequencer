package handlers

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/syncstream/media-sequencer/assets"
	"github.com/syncstream/media-sequencer/internal/httpx"
)

// assetModTime is a fixed timestamp for the embedded files. embed.FS reports a
// zero modification time, which would defeat conditional requests; a constant
// is correct because the bytes only change when the binary does.
var assetModTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// ServeAsset serves the demo media bundled into the binary.
//
// http.ServeContent is used rather than writing the bytes directly because it
// handles Range requests, which is what lets a browser seek within a video.
func (a *API) ServeAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	// Reject anything that is not a plain filename, so no path trickery can
	// reach outside the embedded directory.
	if name == "" || name != path.Base(name) || strings.HasPrefix(name, ".") {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid asset name")
		return
	}

	file, err := assets.FS.Open(name)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "no such asset: "+name)
		return
	}
	defer file.Close()

	readSeeker, ok := file.(io.ReadSeeker)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "asset is not seekable")
		return
	}

	// The bytes are immutable for the life of the deployment.
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	http.ServeContent(w, r, name, assetModTime, readSeeker)
}

// assetNames lists the embedded demo assets, used by /health for visibility.
func assetNames() []string {
	entries, err := fs.ReadDir(assets.FS, ".")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
