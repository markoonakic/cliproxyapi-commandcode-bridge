// Package host bridges plugin-side upstream calls to the CLIProxyAPI host
// callbacks. The C ABI entry point installs a caller, so package code can issue
// host calls without importing cgo.
package host

import (
	"context"
	"errors"
)

// Caller issues a host callback and decodes the result. payload is
// JSON-marshalled into the request; result receives the decoded envelope result
// when non-nil.
type Caller interface {
	Call(method string, payload any, result any) error
}

// Callback carries the host callback id on an RPC request envelope. The host
// requires it to attach request-scoped context, such as the transport policy
// used for host.http.do.
type Callback struct {
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type callbackKey struct{}

var (
	active Caller
	// ErrUnavailable reports that no host caller is installed.
	ErrUnavailable = errors.New("host caller is not available")
)

// Install sets the active host caller. It is called once from the ABI entry point.
func Install(c Caller) {
	active = c
}

// Clear removes the installed caller so late calls fail instead of panicking.
func Clear() {
	active = nil
}

// Call issues a host callback through the installed caller.
func Call(method string, payload any, result any) error {
	if active == nil {
		return ErrUnavailable
	}
	return active.Call(method, payload, result)
}

// WithCallbackID returns a context carrying the host callback id.
func WithCallbackID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, callbackKey{}, id)
}

// CallbackFrom returns the host callback id stored on the context, if any.
func CallbackFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(callbackKey{}).(string)
	return id
}
