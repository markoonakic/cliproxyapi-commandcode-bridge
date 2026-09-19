// Package abi defines the CLIProxyAPI native plugin RPC envelope and method names.
//
// The host loads the compiled shared library, calls cliproxy_plugin_init once,
// then dispatches RPC methods through cliproxyPluginCall. Every request and
// response is a JSON envelope. The capability payload types themselves come from
// the upstream SDK (sdk/pluginapi); this package only carries the framing.
package abi

import (
	"encoding/json"
	"fmt"
)

// ABIVersion is the native C ABI shape. The host rejects a mismatch.
const ABIVersion uint32 = 1

// SchemaVersion is the RPC contract negotiated at plugin.register. Version 6
// matches CLIProxyAPI v7.3.6, the version this plugin is built against.
const SchemaVersion uint32 = 6

// RPC methods dispatched by the host into the plugin.
const (
	MethodPluginRegister    = "plugin.register"
	MethodPluginReconfigure = "plugin.reconfigure"
	MethodPluginShutdown    = "plugin.shutdown"

	MethodModelStatic  = "model.static"
	MethodModelForAuth = "model.for_auth"

	MethodAuthIdentifier = "auth.identifier"
	MethodAuthParse      = "auth.parse"
	MethodAuthLoginStart = "auth.login.start"
	MethodAuthLoginPoll  = "auth.login.poll"
	MethodAuthRefresh    = "auth.refresh"

	MethodSchedulerPick = "scheduler.pick"

	MethodExecutorIdentifier    = "executor.identifier"
	MethodExecutorExecute       = "executor.execute"
	MethodExecutorExecuteStream = "executor.execute_stream"
	MethodExecutorCountTokens   = "executor.count_tokens"
	MethodExecutorHTTPRequest   = "executor.http_request"

	MethodQuotaIdentifier = "quota.identifier"
	MethodQuotaDescribe   = "quota.describe"
	MethodQuotaFetch      = "quota.fetch"
	MethodQuotaReset      = "quota.reset"

	MethodManagementRegister = "management.register"
	MethodManagementHandle   = "management.handle"
)

// Host callback methods the plugin may invoke on the host.
const (
	MethodHostHTTPDo   = "host.http.do"
	MethodHostAuthList = "host.auth.list"
	MethodHostAuthSave = "host.auth.save"
	MethodHostAuthGet  = "host.auth.get"
	MethodHostLog      = "host.log"
)

// Envelope is the JSON RPC frame exchanged with the host in both directions.
type Envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a structured RPC failure.
type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// StatusCode reports the HTTP status carried by the error, if any.
func (e *Error) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.HTTPStatus
}

// PluginError is an error that carries a structured RPC error.
type PluginError struct {
	Err *Error
}

func (e *PluginError) Error() string {
	if e == nil || e.Err == nil {
		return "plugin error"
	}
	return e.Err.Message
}

// StatusCode reports the HTTP status carried by the error, if any.
func (e *PluginError) StatusCode() int {
	if e == nil || e.Err == nil {
		return 0
	}
	return e.Err.HTTPStatus
}

// NewError builds a PluginError from a code, message and HTTP status.
func NewError(code, message string, httpStatus int) *PluginError {
	return &PluginError{Err: &Error{Code: code, Message: message, HTTPStatus: httpStatus}}
}

// OK wraps a value in a successful envelope.
func OK(result any) ([]byte, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal rpc result: %w", err)
	}
	return json.Marshal(Envelope{OK: true, Result: raw})
}

// Fail wraps an error in a failed envelope, preserving a structured error when present.
func Fail(err error) []byte {
	if pe, ok := err.(*PluginError); ok && pe.Err != nil {
		raw, _ := json.Marshal(Envelope{OK: false, Error: pe.Err})
		return raw
	}
	raw, _ := json.Marshal(Envelope{
		OK:    false,
		Error: &Error{Code: "plugin_error", Message: err.Error()},
	})
	return raw
}
