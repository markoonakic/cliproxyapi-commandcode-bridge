// Command commandcode-bridge is a native CLIProxyAPI plugin that gives Command
// Code accounts parity with the built-in antigravity and codex channels:
// quota and limits, credit balances, and account health.
//
// It is built as a C shared library:
//
//	go build -buildmode=c-shared -o commandcode-bridge.so .
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"unsafe"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/abi"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/host"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/plugin"
)

// Build metadata, injected with -ldflags "-X main.Version=... -X main.Commit=...".
var (
	Version = "dev"
	Commit  = "none"
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(hostAPI *C.cliproxy_host_api, pluginAPI *C.cliproxy_plugin_api) C.int {
	if pluginAPI == nil {
		return 1
	}
	C.store_host_api(hostAPI)
	host.Install(cgoCaller{})
	pluginAPI.abi_version = C.uint32_t(abi.ABIVersion)
	pluginAPI.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	pluginAPI.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	pluginAPI.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, abi.Fail(abi.NewError("invalid_method", "method is required", 400)))
		return 1
	}
	var raw []byte
	if request != nil && requestLen > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	rawResult, err := plugin.Dispatch(C.GoString(method), raw, Version, Commit)
	if err != nil {
		writeResponse(response, abi.Fail(err))
		return 1
	}
	writeResponse(response, rawResult)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	plugin.Shutdown()
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

// errHostCallback reports a failed or empty host callback.
var errHostCallback = errors.New("host callback failed")

// cgoCaller implements host.Caller over the C ABI.
type cgoCaller struct{}

func (cgoCaller) Call(method string, payload any, result any) error {
	var rawRequest []byte
	if payload != nil {
		var errMarshal error
		rawRequest, errMarshal = json.Marshal(payload)
		if errMarshal != nil {
			return errMarshal
		}
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var requestPtr *C.uint8_t
	if len(rawRequest) > 0 {
		requestPtr = (*C.uint8_t)(C.CBytes(rawRequest))
		defer C.free(unsafe.Pointer(requestPtr))
	}
	var response C.cliproxy_buffer
	if code := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawRequest)), &response); code != 0 {
		return errHostCallback
	}
	if response.ptr == nil || response.len == 0 {
		return errHostCallback
	}
	rawResponse := C.GoBytes(response.ptr, C.int(response.len))
	C.free_host_buffer(response.ptr, response.len)

	var envelope abi.Envelope
	if errUnmarshal := json.Unmarshal(rawResponse, &envelope); errUnmarshal != nil {
		return errUnmarshal
	}
	if !envelope.OK {
		if envelope.Error == nil {
			return errHostCallback
		}
		return &abi.PluginError{Err: envelope.Error}
	}
	if result == nil || len(envelope.Result) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Result, result)
}
