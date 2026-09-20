package executor

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// streamFixture is a representative Command Code event stream: reasoning, text,
// a whole tool call, and a terminal finish carrying usage.
const streamFixture = `data: {"type":"reasoning-delta","text":"Thinking..."}
data: {"type":"text-delta","text":"Hello"}
data: {"type":"text-delta","text":" world"}
data: {"type":"tool-call","toolCallId":"call_1","toolName":"get_weather","input":{"city":"Paris"}}
data: {"type":"finish","finishReason":"tool-calls","totalUsage":{"inputTokens":10,"outputTokens":5,"totalTokens":15}}

`

func collect(t *testing.T, includeUsage bool) [][]byte {
	t.Helper()
	state := newResponseState("chatcmpl-test", 1700000000, "deepseek/deepseek-v4.1-flash", includeUsage)
	frames, err := state.Feed([]byte(streamFixture))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}
	tail, err := state.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}
	return append(frames, tail...)
}

func frameText(t *testing.T, frame []byte) string {
	t.Helper()
	trimmed := strings.TrimSpace(string(frame))
	return strings.TrimPrefix(trimmed, "data: ")
}

// isDoneMarker reports whether a frame is the closing SSE marker, which is not
// a JSON chunk.
func isDoneMarker(frame []byte) bool {
	return strings.TrimSpace(string(frame)) == "data: [DONE]"
}

func parseFrame(t *testing.T, frame []byte) openAIChunk {
	t.Helper()
	var chunk openAIChunk
	if err := json.Unmarshal([]byte(frameText(t, frame)), &chunk); err != nil {
		t.Fatalf("frame is not a chunk: %v (%s)", err, frame)
	}
	return chunk
}

func TestStreamTranslationProducesChunks(t *testing.T) {
	frames := collect(t, true)
	if len(frames) < 4 {
		t.Fatalf("frames = %d, want at least 4", len(frames))
	}

	// The first frame must announce the assistant role.
	first := parseFrame(t, frames[0])
	if first.Object != streamObjectName {
		t.Errorf("object = %q, want %q", first.Object, streamObjectName)
	}
	if first.Choices[0].Delta["role"] != "assistant" {
		t.Errorf("first delta role = %v, want assistant", first.Choices[0].Delta["role"])
	}
	if first.Choices[0].Delta["reasoning_content"] != "Thinking..." {
		t.Errorf("reasoning delta = %v", first.Choices[0].Delta["reasoning_content"])
	}

	// Text deltas must be forwarded in order.
	var text strings.Builder
	for _, frame := range frames {
		if isDoneMarker(frame) {
			continue
		}
		chunk := parseFrame(t, frame)
		if content, ok := chunk.Choices[0].Delta["content"].(string); ok {
			text.WriteString(content)
		}
	}
	if text.String() != "Hello world" {
		t.Errorf("accumulated text = %q, want %q", text.String(), "Hello world")
	}

	// The closing marker must be present.
	last := strings.TrimSpace(string(frames[len(frames)-1]))
	if last != "data: [DONE]" {
		t.Errorf("last frame = %q, want data: [DONE]", last)
	}
}

func TestStreamToolCallAndFinishReason(t *testing.T) {
	frames := collect(t, false)
	var sawToolCall bool
	var finishReason string
	for _, frame := range frames {
		if isDoneMarker(frame) {
			continue
		}
		chunk := parseFrame(t, frame)
		delta := chunk.Choices[0].Delta
		if calls, ok := delta["tool_calls"].([]any); ok && len(calls) > 0 {
			call, _ := calls[0].(map[string]any)
			if call["id"] == "call_1" {
				sawToolCall = true
				fn, _ := call["function"].(map[string]any)
				if fn["name"] != "get_weather" {
					t.Errorf("tool name = %v", fn["name"])
				}
				// The arguments must be compact JSON, not a re-quoted string.
				args, _ := fn["arguments"].(string)
				if args != `{"city":"Paris"}` {
					t.Errorf("tool arguments = %q", args)
				}
			}
		}
		if chunk.Choices[0].FinishReason != nil {
			finishReason = *chunk.Choices[0].FinishReason
		}
	}
	if !sawToolCall {
		t.Error("tool call frame was not emitted")
	}
	if finishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", finishReason)
	}
}

// TestUsageOnlyWhenRequested verifies the terminal frame carries usage only when
// the client asked for it, matching OpenAI stream_options semantics.
func TestUsageOnlyWhenRequested(t *testing.T) {
	withUsage := collect(t, true)
	var found *openAIUsage
	for _, frame := range withUsage {
		if isDoneMarker(frame) {
			continue
		}
		chunk := parseFrame(t, frame)
		if chunk.Usage != nil {
			found = chunk.Usage
		}
	}
	if found == nil {
		t.Fatal("expected a usage object when include_usage is true")
	}
	if found.PromptTokens != 10 || found.CompletionTokens != 5 || found.TotalTokens != 15 {
		t.Errorf("usage = %+v, want prompt 10 completion 5 total 15", found)
	}

	for _, frame := range collect(t, false) {
		if isDoneMarker(frame) {
			continue
		}
		if parseFrame(t, frame).Usage != nil {
			t.Error("usage must be omitted when include_usage is false")
		}
	}
}

func TestCompletionRendering(t *testing.T) {
	state := newResponseState("chatcmpl-test", 1700000000, "m", true)
	if _, err := state.Feed([]byte(streamFixture)); err != nil {
		t.Fatalf("Feed failed: %v", err)
	}
	if _, err := state.Finish(); err != nil {
		t.Fatalf("Finish failed: %v", err)
	}
	raw, err := state.Completion()
	if err != nil {
		t.Fatalf("Completion failed: %v", err)
	}
	var body struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []any  `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *openAIUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("completion is not valid JSON: %v", err)
	}
	if body.Object != "chat.completion" {
		t.Errorf("object = %q", body.Object)
	}
	message := body.Choices[0].Message
	if message.Content != "Hello world" {
		t.Errorf("content = %q", message.Content)
	}
	if message.ReasoningContent != "Thinking..." {
		t.Errorf("reasoning = %q", message.ReasoningContent)
	}
	if len(message.ToolCalls) != 1 {
		t.Errorf("tool_calls = %d, want 1", len(message.ToolCalls))
	}
	if body.Usage == nil || body.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", body.Usage)
	}
}

// TestStreamWithoutTerminalEventFails verifies a truncated stream is reported
// rather than silently producing a partial success.
func TestStreamWithoutTerminalEventFails(t *testing.T) {
	state := newResponseState("id", 0, "m", false)
	if _, err := state.Feed([]byte("data: {\"type\":\"text-delta\",\"text\":\"hi\"}\n")); err != nil {
		t.Fatalf("Feed failed: %v", err)
	}
	if _, err := state.Finish(); err == nil {
		t.Fatal("expected an error for a stream with no terminal event")
	}
}

func TestUpstreamErrorEventFails(t *testing.T) {
	state := newResponseState("id", 0, "m", false)
	_, err := state.Feed([]byte("data: {\"type\":\"error\",\"message\":\"upstream exploded\"}\n"))
	if err == nil {
		t.Fatal("expected an error from an upstream error event")
	}
	if !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %v", err)
	}
}

// TestUnknownEventTypesAreIgnored protects against upstream event additions.
func TestUnknownEventTypesAreIgnored(t *testing.T) {
	state := newResponseState("id", 0, "m", false)
	if _, err := state.Feed([]byte("data: {\"type\":\"brand-new-event\",\"x\":1}\n")); err != nil {
		t.Fatalf("unknown event must not fail the stream: %v", err)
	}
}

func TestNormalizedFinishReason(t *testing.T) {
	cases := map[string]string{
		"stop": "stop", "tool-calls": "tool_calls", "tool_calls": "tool_calls",
		"length": "length", "max_tokens": "length", "max_output_tokens": "length",
		"": "stop", "something-else": "stop",
	}
	for input, want := range cases {
		state := &responseState{finishReason: input}
		if got := state.normalizedFinishReason(); got != want {
			t.Errorf("normalizedFinishReason(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestNewIDIsUUID guards the upstream wire format. Command Code rejects a bare
// hex string with HTTP 400, so the id must be a grouped v4 UUID.
func TestNewIDIsUUID(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := newID()
		if !pattern.MatchString(id) {
			t.Fatalf("newID() = %q, want an RFC 4122 v4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("newID() repeated %q", id)
		}
		seen[id] = true
	}
}

// TestEmittedPayloadStripsDataPrefix guards the SSE wire format.
//
// Frames are built with a "data: " prefix, but the host re-adds that prefix
// when it writes the event. Emitting it here produced "data: data: {...}" and
// broke SSE parsing on the client.
func TestEmittedPayloadStripsDataPrefix(t *testing.T) {
	stripped := func(frame []byte) (string, bool) {
		payload := strings.TrimSpace(string(frame))
		if payload == "[DONE]" {
			return "", false
		}
		if after, ok := strings.CutPrefix(payload, "data:"); ok {
			payload = strings.TrimSpace(after)
			if payload == "" || payload == "[DONE]" {
				return "", false
			}
		}
		return payload, len(payload) > 0
	}

	state := newResponseState("id", 0, "m", false)
	frames, err := state.Feed([]byte("data: {\"type\":\"text-delta\",\"text\":\"hi\"}\n"))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}
	var emitted []string
	for _, frame := range frames {
		if payload, ok := stripped(frame); ok {
			emitted = append(emitted, payload)
		}
	}
	if len(emitted) == 0 {
		t.Fatal("expected at least one emitted frame")
	}
	for _, payload := range emitted {
		if strings.HasPrefix(payload, "data:") {
			t.Errorf("emitted payload still has a data: prefix: %q", payload)
		}
		if !strings.HasPrefix(payload, "{") {
			t.Errorf("emitted payload is not JSON: %q", payload)
		}
	}
}
