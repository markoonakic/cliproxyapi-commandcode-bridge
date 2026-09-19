// Package plugin wires the CLIProxyAPI capability implementations into the
// host's RPC method dispatch.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/abi"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/auth"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/host"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/management"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/model"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/quota"
)

// runtime holds the capability implementations for the current run.
type runtime struct {
	quota      *quota.Provider
	auth       *auth.Provider
	models     *model.Provider
	management *management.Provider
}

func newRuntime() *runtime {
	quotaProvider := quota.NewProvider()
	return &runtime{
		quota:      quotaProvider,
		auth:       auth.NewProvider(),
		models:     model.NewProvider(),
		management: management.NewProvider(quotaProvider),
	}
}

var state = newRuntime()

// Reset rebuilds runtime state. It is used by tests.
func Reset() {
	state = newRuntime()
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

	// Quota capability.
	case abi.MethodQuotaIdentifier:
		return abi.OK(map[string]string{"identifier": state.quota.Identifier()})
	case abi.MethodQuotaDescribe:
		req, err := decode[pluginapi.QuotaDescribeRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		return respond(state.quota.DescribeQuota(context.Background(), req))
	case abi.MethodQuotaFetch:
		req, callbackID, err := decodeWithCallback[pluginapi.QuotaFetchRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		return respond(state.quota.FetchQuota(host.WithCallbackID(context.Background(), callbackID), req))
	case abi.MethodQuotaReset:
		req, callbackID, err := decodeWithCallback[pluginapi.QuotaResetRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		return respond(state.quota.ResetQuota(host.WithCallbackID(context.Background(), callbackID), req))

	// Auth capability.
	case abi.MethodAuthIdentifier:
		return abi.OK(map[string]string{"identifier": state.auth.Identifier()})
	case abi.MethodAuthParse:
		req, err := decode[pluginapi.AuthParseRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		return respond(state.auth.ParseAuth(context.Background(), req))
	case abi.MethodAuthLoginStart:
		req, err := decode[pluginapi.AuthLoginStartRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		// Command Code has no OAuth flow; report the boundary as a 501.
		if _, errStart := state.auth.StartLogin(context.Background(), req); errStart != nil {
			return abi.Fail(abi.NewError("not_supported", errStart.Error(), 501)), nil
		}
		return abi.OK(pluginapi.AuthLoginStartResponse{})
	case abi.MethodAuthLoginPoll:
		req, err := decode[pluginapi.AuthLoginPollRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		return respond(state.auth.PollLogin(context.Background(), req))
	case abi.MethodAuthRefresh:
		req, callbackID, err := decodeWithCallback[pluginapi.AuthRefreshRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		return respond(state.auth.RefreshAuth(host.WithCallbackID(context.Background(), callbackID), req))

	// Model capability.
	case abi.MethodModelStatic:
		req, err := decode[pluginapi.StaticModelRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		return respond(state.models.StaticModels(context.Background(), req))
	case abi.MethodModelForAuth:
		req, callbackID, err := decodeWithCallback[pluginapi.AuthModelRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		return respond(state.models.ModelsForAuth(host.WithCallbackID(context.Background(), callbackID), req))

	// Management capability.
	case abi.MethodManagementRegister:
		req, err := decode[pluginapi.ManagementRegistrationRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		resp, errRegister := state.management.RegisterManagement(context.Background(), req)
		if errRegister != nil {
			return abi.Fail(errRegister), nil
		}
		// Handlers are injected by the host, so strip them from the wire payload.
		return abi.OK(managementRegistrationWire(resp))
	case abi.MethodManagementHandle:
		req, callbackID, err := decodeWithCallback[pluginapi.ManagementRequest](raw)
		if err != nil {
			return abi.Fail(err), nil
		}
		return respond(state.management.HandleManagement(host.WithCallbackID(context.Background(), callbackID), req))

	default:
		return abi.Fail(abi.NewError("unknown_method", fmt.Sprintf("unknown method: %s", method), 400)), nil
	}
}

// managementRouteWire is one management route on the wire. Handlers are
// interfaces and never cross the ABI.
type managementRouteWire struct {
	Method      string `json:"Method,omitempty"`
	Path        string `json:"Path,omitempty"`
	Menu        string `json:"Menu,omitempty"`
	Description string `json:"Description,omitempty"`
}

type managementResourceWire struct {
	Path        string `json:"Path,omitempty"`
	Menu        string `json:"Menu,omitempty"`
	Description string `json:"Description,omitempty"`
}

func managementRegistrationWire(resp pluginapi.ManagementRegistrationResponse) map[string]any {
	out := map[string]any{}
	if len(resp.Routes) > 0 {
		routes := make([]managementRouteWire, 0, len(resp.Routes))
		for _, route := range resp.Routes {
			routes = append(routes, managementRouteWire{
				Method:      route.Method,
				Path:        route.Path,
				Menu:        route.Menu,
				Description: route.Description,
			})
		}
		out["routes"] = routes
	}
	if len(resp.Resources) > 0 {
		resources := make([]managementResourceWire, 0, len(resp.Resources))
		for _, route := range resp.Resources {
			resources = append(resources, managementResourceWire{
				Path:        route.Path,
				Menu:        route.Menu,
				Description: route.Description,
			})
		}
		out["resources"] = resources
	}
	return out
}

// respond wraps a capability result in an envelope, converting an error into a
// failed envelope so the host surfaces it instead of a silent empty success.
func respond[T any](value T, err error) ([]byte, error) {
	if err != nil {
		return abi.Fail(err), nil
	}
	return abi.OK(value)
}

// callbackEnvelope carries the host callback id the host appends to capability
// requests. The SDK payload types do not declare it.
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
