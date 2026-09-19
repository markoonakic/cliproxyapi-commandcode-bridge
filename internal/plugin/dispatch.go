// Package plugin wires the CLIProxyAPI capability implementations into the
// host's RPC method dispatch.
package plugin

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/abi"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/host"
)

type runtimeState struct {
	quota *quotaProvider
}

var state = &runtimeState{
	quota: newQuotaProvider(),
}

// Reset clears runtime state. It is used by tests.
func Reset() {
	state = &runtimeState{quota: newQuotaProvider()}
}

// Shutdown releases runtime resources and prevents late host calls.
func Shutdown() {
	host.Clear()
}

// Dispatch routes one host RPC method to its implementation.
func Dispatch(method string, raw []byte, version, commit string) ([]byte, error) {
	switch method {
	case abi.MethodPluginRegister, abi.MethodPluginReconfigure:
		return registration(version, commit)
	case abi.MethodPluginShutdown:
		Shutdown()
		return abi.OK(struct{}{})
	case abi.MethodQuotaIdentifier:
		return abi.OK(map[string]string{"identifier": state.quota.Identifier()})
	case abi.MethodQuotaDescribe:
		req, err := decode[pluginapi.QuotaDescribeRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		resp, errCall := state.quota.DescribeQuota(contextBackground(), req)
		if errCall != nil {
			return abi.Fail(errCall), nil
		}
		return abi.OK(resp)
	case abi.MethodQuotaFetch:
		req, callbackID, err := decodeWithCallback[pluginapi.QuotaFetchRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		ctx := host.WithCallbackID(contextBackground(), callbackID)
		resp, errCall := state.quota.FetchQuota(ctx, req)
		if errCall != nil {
			return abi.Fail(errCall), nil
		}
		return abi.OK(resp)
	case abi.MethodQuotaReset:
		req, callbackID, err := decodeWithCallback[pluginapi.QuotaResetRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		ctx := host.WithCallbackID(contextBackground(), callbackID)
		resp, errCall := state.quota.ResetQuota(ctx, req)
		if errCall != nil {
			return abi.Fail(errCall), nil
		}
		return abi.OK(resp)
	default:
		return abi.Fail(abi.NewError("unknown_method", fmt.Sprintf("unknown method: %s", method), 400)), nil
	}
}

// callbackEnvelope carries the host callback id the host appends to
// capability requests. The SDK payload types do not declare it, so it is
// decoded separately and attached to the call context.
type callbackEnvelope struct {
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func decodeWithCallback[T any](raw []byte) (T, string, error) {
	out, err := decode[T](raw)
	if err != nil {
		var zero T
		return zero, "", err
	}
	var envelope callbackEnvelope
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &envelope)
	}
	return out, envelope.HostCallbackID, nil
}

func decode[T any](raw []byte) (T, error) {
	var out T
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, abi.NewError("invalid_request", "invalid request payload", 400)
	}
	return out, nil
}
