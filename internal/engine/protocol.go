package engine

import (
	"context"
	"encoding/json"
)

// Checker defines what every probe type must do.
//
// To add a new probe type:
//  1. Create a new file (e.g. grpc.go) with a struct implementing Checker.
//  2. Register it in New() inside the checkers map.
//  3. No other file needs to change.
type Checker interface {
	// Run tests whether addr is reachable. Returns (true, nil) on success.
	Run(ctx context.Context, addr string, opts json.RawMessage) (bool, error)

	// ValidateOptions verifies opts at config-load time.
	// Implementations must reject unknown JSON fields.
	ValidateOptions(opts json.RawMessage) error

	// ParseAddr splits addr into (host, port) for use in alert payloads.
	ParseAddr(addr string) (host, port string, err error)
}
