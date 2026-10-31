// Package domain is the core of idrometer. It imports only the Go standard
// library; adapters (storage, HTTP, clients) depend on it, never the reverse.
package domain

import "errors"

// Sentinel errors. Adapters map these to transport-specific responses.
var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid input")
	ErrUnauthorized = errors.New("unauthorized")
)
