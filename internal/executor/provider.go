// Package executor implements the CLIProxyAPI Executor capability for Command
// Code.
package executor

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/auth"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/host"
)

// ProviderID is the stable provider key.
const ProviderID = auth.ProviderID

// upstreamBaseURL is the Command Code API root.
const upstreamBaseURL = "https://api.commandcode.ai"

// executorRequest is the inbound RPC payload. The host appends stream_id and
// host_callback_id as flat siblings of the SDK request.
type executorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// Provider implements pluginapi.ProviderExecutor.
type Provider struct {
	shuttingDown bool
	mu           sync.Mutex
}

// NewProvider builds an executor provider.
func NewProvider() *Provider {
	return &Provider{}
}

// Identifier returns the provider key handled by this executor.
func (p *Provider) Identifier() string { return ProviderID }

// Execute runs a non-streaming request by consuming the upstream stream and
// rendering a single completion body.
func (p *Provider) Execute(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	prepared, err := prepare(ctx, req)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	stream, err := openStream(prepared)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	defer stream.Close()

	state := newResponseState("chatcmpl-"+newID(), time.Now().Unix(), prepared.model, true)
	if err := drain(stream, state); err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	if _, err := state.Finish(); err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	completion, err := state.Completion()
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	return pluginapi.ExecutorResponse{
		Payload: completion,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// ExecuteStream streams upstream events to the host as OpenAI chunks.
//
// The upstream read happens on a worker goroutine so the RPC can return headers
// immediately, which is the contract the host expects for asynchronous
// streaming. Chunks are delivered with host.stream.emit, and the stream is
// always closed.
func (p *Provider) ExecuteStream(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
	streamID := host.StreamIDFrom(ctx)
	if strings.TrimSpace(streamID) == "" {
		return pluginapi.ExecutorStreamResponse{}, fmt.Errorf("stream_id is required")
	}

	prepared, err := prepare(ctx, req)
	if err != nil {
		return pluginapi.ExecutorStreamResponse{}, err
	}

	stream, err := openStream(prepared)
	if err != nil {
		return pluginapi.ExecutorStreamResponse{}, err
	}

	if !p.startWorker(stream.Close, func() {
		runStreamWorker(prepared, stream, streamID)
	}) {
		stream.Close()
		return pluginapi.ExecutorStreamResponse{}, fmt.Errorf("plugin is shutting down")
	}

	return pluginapi.ExecutorStreamResponse{
		Headers: http.Header{
			"Content-Type":  []string{"text/event-stream"},
			"Cache-Control": []string{"no-cache"},
		},
	}, nil
}

// CountTokens is unsupported: Command Code exposes no token counting endpoint.
func (p *Provider) CountTokens(_ context.Context, _ pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	return pluginapi.ExecutorResponse{}, fmt.Errorf("CommandCode token counting is not supported")
}

// HttpRequest proxies a raw upstream request with the credential's key
// substituted, for endpoints the generic path needs to reach.
func (p *Provider) HttpRequest(ctx context.Context, req pluginapi.ExecutorHTTPRequest) (pluginapi.ExecutorHTTPResponse, error) {
	cred, err := auth.Parse(req.StorageJSON)
	if err != nil {
		return pluginapi.ExecutorHTTPResponse{}, err
	}
	headers := http.Header{}
	for key, values := range req.Headers {
		headers[key] = append([]string(nil), values...)
	}
	headers.Set("Authorization", "Bearer "+cred.APIKey)

	var response pluginapi.HTTPResponse
	if err := host.Call(pluginabi.MethodHostHTTPDo, map[string]any{
		"host_callback_id": host.CallbackFrom(ctx),
		"method":           req.Method,
		"url":              req.URL,
		"headers":          headers,
		"body":             req.Body,
	}, &response); err != nil {
		return pluginapi.ExecutorHTTPResponse{}, err
	}
	return pluginapi.ExecutorHTTPResponse{
		StatusCode: response.StatusCode,
		Headers:    response.Headers,
		Body:       response.Body,
	}, nil
}

// prepared carries everything the stream worker needs for one request.
type prepared struct {
	model      string
	apiKey     string
	body       []byte
	options    requestOptions
	callbackID string
}

func prepare(ctx context.Context, req pluginapi.ExecutorRequest) (prepared, error) {
	cred, err := auth.Parse(req.StorageJSON)
	if err != nil {
		return prepared{}, err
	}
	payload := req.Payload
	if len(payload) == 0 {
		payload = req.OriginalRequest
	}
	body, options, err := translateRequest(payload, req.Model, newID(), cred, time.Now())
	if err != nil {
		return prepared{}, err
	}
	model := resolveModel(req.Model, "", cred)
	return prepared{
		model:      model,
		apiKey:     cred.APIKey,
		body:       body,
		options:    options,
		callbackID: host.CallbackFrom(ctx),
	}, nil
}

// upstreamStream reads the host-owned upstream HTTP stream.
type upstreamStream struct {
	id   string
	once sync.Once
}

type hostHTTPStreamResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers,omitempty"`
	StreamID   string      `json:"stream_id,omitempty"`
}

type hostHTTPStreamReadResponse struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

// openStream starts the upstream request as a host-owned stream.
func openStream(req prepared) (*upstreamStream, error) {
	headers := http.Header{
		"Authorization":          []string{"Bearer " + req.apiKey},
		"Content-Type":           []string{"application/json"},
		"X-Cli-Environment":      []string{"production"},
		"X-Command-Code-Version": []string{cliVersion},
		"X-Session-Id":           []string{newID()},
	}
	var response hostHTTPStreamResponse
	err := host.Call(pluginabi.MethodHostHTTPDoStream, map[string]any{
		"host_callback_id": req.callbackID,
		"method":           http.MethodPost,
		"url":              upstreamBaseURL + generatePath,
		"headers":          headers,
		"body":             req.body,
	}, &response)
	if err != nil {
		return nil, fmt.Errorf("CommandCode request failed: %w", err)
	}
	stream := &upstreamStream{id: response.StreamID}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		stream.Close()
		return nil, upstreamStatusError(response.StatusCode)
	}
	if strings.TrimSpace(response.StreamID) == "" {
		return nil, fmt.Errorf("CommandCode response stream is unavailable")
	}
	return stream, nil
}

func (s *upstreamStream) Read() (hostHTTPStreamReadResponse, error) {
	var response hostHTTPStreamReadResponse
	if s == nil || s.id == "" {
		return response, fmt.Errorf("upstream stream is closed")
	}
	if err := host.Call(pluginabi.MethodHostHTTPStreamRead, map[string]any{"stream_id": s.id}, &response); err != nil {
		return response, err
	}
	if response.Error != "" {
		return response, fmt.Errorf("%s", response.Error)
	}
	return response, nil
}

// Close releases the host-owned stream exactly once.
func (s *upstreamStream) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.id != "" {
			_ = host.Call(pluginabi.MethodHostHTTPStreamClose, map[string]any{"stream_id": s.id}, &struct{}{})
		}
	})
}

// drain reads the upstream stream to completion, feeding the response state.
func drain(stream *upstreamStream, state *responseState) error {
	for {
		chunk, err := stream.Read()
		if err != nil {
			return fmt.Errorf("CommandCode stream failed: %w", err)
		}
		if len(chunk.Payload) > 0 {
			if _, errFeed := state.Feed(chunk.Payload); errFeed != nil {
				return errFeed
			}
		}
		if chunk.Done {
			return nil
		}
	}
}

// runStreamWorker emits translated frames to the host stream until completion.
func runStreamWorker(req prepared, stream *upstreamStream, streamID string) {
	var closeError string
	defer func() {
		stream.Close()
		payload := map[string]any{"stream_id": streamID}
		if closeError != "" {
			payload["error"] = closeError
		}
		_ = host.Call(pluginabi.MethodHostStreamClose, payload, &struct{}{})
	}()

	state := newResponseState("chatcmpl-"+newID(), time.Now().Unix(), req.model, req.options.IncludeUsage)
	for {
		chunk, err := stream.Read()
		if err != nil {
			closeError = "CommandCode stream failed"
			return
		}
		if len(chunk.Payload) > 0 {
			frames, errFeed := state.Feed(chunk.Payload)
			if errFeed != nil {
				closeError = errFeed.Error()
				return
			}
			if errEmit := emitFrames(streamID, frames); errEmit != nil {
				closeError = "downstream stream closed"
				return
			}
		}
		if chunk.Done {
			break
		}
	}
	frames, errFinish := state.Finish()
	if errFinish != nil {
		closeError = errFinish.Error()
		return
	}
	if errEmit := emitFrames(streamID, frames); errEmit != nil {
		closeError = "downstream stream closed"
	}
}

func emitFrames(streamID string, frames [][]byte) error {
	for _, frame := range frames {
		if err := emitDownstream(streamID, frame); err != nil {
			return err
		}
	}
	return nil
}

// emitDownstream forwards one OpenAI SSE frame to the host.
//
// The "data: " prefix is stripped: the host re-adds it when it writes the
// event, so sending it here produced a doubled "data: data:" line that breaks
// SSE parsing on the client. The closing marker is dropped because the host
// stream close signals the end.
func emitDownstream(streamID string, payload []byte) error {
	payload = bytesTrimSpace(payload)
	if len(payload) == 0 {
		return nil
	}
	if string(payload) == "[DONE]" {
		return nil
	}
	if after, ok := strings.CutPrefix(string(payload), "data:"); ok {
		payload = bytesTrimSpace([]byte(after))
		if len(payload) == 0 || string(payload) == "[DONE]" {
			return nil
		}
	}
	return host.Call(pluginabi.MethodHostStreamEmit, map[string]any{
		"stream_id": streamID,
		"payload":   payload,
	}, &struct{}{})
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// upstreamStatusError maps an upstream status to a classified error.
func upstreamStatusError(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("CommandCode rejected the credential (http %d)", status)
	case http.StatusForbidden:
		return fmt.Errorf("CommandCode rejected the request (http %d)", status)
	case http.StatusTooManyRequests:
		return fmt.Errorf("CommandCode rate limit exceeded (http %d)", status)
	default:
		return fmt.Errorf("CommandCode returned HTTP %d", status)
	}
}

// startWorker runs a stream worker unless the plugin is shutting down.
func (p *Provider) startWorker(cancel func(), run func()) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shuttingDown {
		return false
	}
	go run()
	return true
}

// Shutdown prevents new workers from starting.
func (p *Provider) Shutdown() {
	p.mu.Lock()
	p.shuttingDown = true
	p.mu.Unlock()
}

// newID returns a random RFC 4122 version 4 UUID.
//
// The format matters: Command Code rejects a bare hex string with HTTP 400, so
// the version and variant bits are set and the value is grouped 8-4-4-4-12.
func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}
