// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"errors"
)

// Priority orders requests when a backend has a quota (Google Sheets allows
// about 60 requests per minute). When quota runs low, lower priorities wait
// first so the people at the door keep scanning.
type Priority int

const (
	PriorityBackground Priority = iota // form import, outbox
	PriorityAdmin                      // control panel
	PriorityPortal                     // participants and registration
	PriorityScanner                    // staff at a checkpoint
)

func (p Priority) String() string {
	return [...]string{"background", "admin", "portal", "scanner"}[p]
}

type priorityKey struct{}

// WithPriority tags ctx with p.
func WithPriority(ctx context.Context, p Priority) context.Context {
	return context.WithValue(ctx, priorityKey{}, p)
}

// PriorityFrom returns the priority on ctx, PriorityAdmin by default.
func PriorityFrom(ctx context.Context) Priority {
	if p, ok := ctx.Value(priorityKey{}).(Priority); ok {
		return p
	}
	return PriorityAdmin
}

// ErrNotConnected means an event has no spreadsheet connected yet.
var ErrNotConnected = errors.New("event spreadsheet not connected")
