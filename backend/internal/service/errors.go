// Package service holds the application logic that sits between the HTTP
// handlers and the repositories.
package service

import (
	"strings"

	"github.com/syncstream/media-sequencer/internal/models"
)

// ValidationFailure aggregates field errors so the API can report all problems
// with a request at once instead of one per round trip.
type ValidationFailure struct {
	Errors []models.ValidationError
}

func (v *ValidationFailure) Error() string {
	parts := make([]string, 0, len(v.Errors))
	for _, e := range v.Errors {
		parts = append(parts, e.Error())
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// invalid is a small constructor for a single-field failure.
func invalid(field, message string) *ValidationFailure {
	return &ValidationFailure{Errors: []models.ValidationError{{Field: field, Message: message}}}
}
