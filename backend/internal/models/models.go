// Package models holds the domain types shared by the repository, service and
// HTTP layers, together with their validation rules.
package models

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// MediaType enumerates the kinds of media a window can display.
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
	// MediaTypeBlank renders a deliberate blank/black frame. Blank playback only
	// ever happens because an operator configured a blank item in a playlist.
	MediaTypeBlank MediaType = "blank"
)

// MinDurationSeconds and MaxDurationSeconds bound a media item's configured
// duration. The upper bound keeps a single item from swallowing a whole cycle.
const (
	MinDurationSeconds = 1
	MaxDurationSeconds = 3600
)

// MaxSyncDurationSeconds bounds a global sync override.
const MaxSyncDurationSeconds = 3600

func (t MediaType) Valid() bool {
	switch t {
	case MediaTypeImage, MediaTypeVideo, MediaTypeBlank:
		return true
	}
	return false
}

// RequiresURL reports whether this media type is meaningless without a source.
func (t MediaType) RequiresURL() bool {
	return t == MediaTypeImage || t == MediaTypeVideo
}

// Window is one display surface with its own playlist and 5-hour cycle anchor.
type Window struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// CycleAnchor is the instant from which this window's 5-hour cycles are
	// measured. It is persisted so playback survives restarts and is identical
	// for every client rendering the window.
	CycleAnchor time.Time `json:"cycleAnchor"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Media is a reusable item in the media library.
type Media struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Type            MediaType `json:"type"`
	URL             string    `json:"url"`
	DurationSeconds int       `json:"durationSeconds"`
	CreatedAt       time.Time `json:"createdAt"`
}

// DurationMillis is the duration in the integer milliseconds used by the
// playback clock.
func (m Media) DurationMillis() int64 { return int64(m.DurationSeconds) * 1000 }

// PlaylistItem places a media item at a position in a window's playlist.
type PlaylistItem struct {
	ID        int64     `json:"id"`
	WindowID  int64     `json:"windowId"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"createdAt"`
	Media     Media     `json:"media"`
}

// WindowWithPlaylist is the shape the API returns for playback configuration.
type WindowWithPlaylist struct {
	Window
	Playlist []PlaylistItem `json:"playlist"`
	// PlaylistDurationMillis is the length of one complete pass; the frontend
	// uses it for display only, never as the source of truth for timing.
	PlaylistDurationMillis int64 `json:"playlistDurationMillis"`
}

// SyncEvent is a temporary global override that shows one media item on every
// window between StartAt and EndAt. It never modifies any playlist.
type SyncEvent struct {
	ID          int64      `json:"id"`
	Media       Media      `json:"media"`
	StartAt     time.Time  `json:"startAt"`
	EndAt       time.Time  `json:"endAt"`
	CancelledAt *time.Time `json:"cancelledAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// ValidationError carries a field-level problem back to the HTTP layer.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string { return e.Field + ": " + e.Message }

// ErrNotFound is returned by repositories when a row does not exist.
var ErrNotFound = errors.New("not found")

// MediaInput is the payload for creating media.
type MediaInput struct {
	Name            string    `json:"name"`
	Type            MediaType `json:"type"`
	URL             string    `json:"url"`
	DurationSeconds int       `json:"durationSeconds"`
}

// Normalize trims user input and clears fields that do not apply to the type.
func (in *MediaInput) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Type = MediaType(strings.ToLower(strings.TrimSpace(string(in.Type))))
	in.URL = strings.TrimSpace(in.URL)
	if in.Type == MediaTypeBlank {
		in.URL = ""
	}
}

// Validate reports every problem with the input at once.
func (in MediaInput) Validate() []ValidationError {
	var errs []ValidationError
	if in.Name == "" {
		errs = append(errs, ValidationError{"name", "name is required"})
	} else if len(in.Name) > 120 {
		errs = append(errs, ValidationError{"name", "name must be at most 120 characters"})
	}
	if !in.Type.Valid() {
		errs = append(errs, ValidationError{"type", "type must be one of: image, video, blank"})
	}
	if in.Type.RequiresURL() {
		if in.URL == "" {
			errs = append(errs, ValidationError{"url", fmt.Sprintf("url is required for %s media", in.Type)})
		} else if err := validateMediaURL(in.URL); err != nil {
			errs = append(errs, ValidationError{"url", err.Error()})
		}
	}
	if in.DurationSeconds < MinDurationSeconds || in.DurationSeconds > MaxDurationSeconds {
		errs = append(errs, ValidationError{"durationSeconds", fmt.Sprintf("duration must be between %d and %d seconds", MinDurationSeconds, MaxDurationSeconds)})
	}
	return errs
}

// validateMediaURL accepts three forms, all of which let the seeded demo run
// without depending on a third-party media host:
//
//   - absolute http(s) URLs, for real media;
//   - inline data: URIs, used by the generated seed images;
//   - root-relative paths such as /api/assets/clip.mp4, which the frontend
//     resolves against its configured API URL. Storing these relative keeps the
//     database portable between local, staging and production deployments.
func validateMediaURL(raw string) error {
	if strings.HasPrefix(raw, "data:") {
		if !strings.Contains(raw, ",") {
			return errors.New("malformed data URI")
		}
		return nil
	}
	if strings.HasPrefix(raw, "/") {
		if strings.HasPrefix(raw, "//") {
			return errors.New("url must be absolute, a data: URI, or a root-relative path")
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must use http, https or be a data: URI")
	}
	if u.Host == "" {
		return errors.New("url must include a host")
	}
	return nil
}
