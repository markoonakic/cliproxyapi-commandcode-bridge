// Package executor implements the CLIProxyAPI Executor capability for Command
// Code, translating OpenAI chat-completions requests to the Command Code
// /alpha/generate wire format and translating its event stream back.
package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/auth"
)

// Endpoints and client identity presented upstream.
const (
	generatePath     = "/alpha/generate"
	cliVersion       = "1.26.0"
	maxEventBytes    = 8 * 1024 * 1024
	permissionMode   = "standard"
	streamObjectName = "chat.completion.chunk"
)

// requestOptions captures the behaviour the response state needs.
type requestOptions struct {
	Stream       bool
	IncludeUsage bool
}

// chatRequest is the inbound OpenAI chat-completions payload we accept.
type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	Tools               []chatTool    `json:"tools"`
	MaxTokens           *int64        `json:"max_tokens"`
	MaxCompletionTokens *int64        `json:"max_completion_tokens"`
	Temperature         *float64      `json:"temperature"`
	TopP                *float64      `json:"top_p"`
	ReasoningEffort     string        `json:"reasoning_effort"`
	Stream              bool          `json:"stream"`
	StreamOptions       streamOptions `json:"stream_options"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []chatToolCall  `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// translateRequest converts an OpenAI chat-completions body into the Command
// Code envelope, returning the options the response state needs.
//
// The model is resolved through the credential's alias map so a client-visible
// alias reaches the correct upstream model, but execution is permissive: an
// unrecognized model is forwarded unchanged rather than rejected locally.
func translateRequest(raw []byte, executorModel, sessionID string, cred auth.Credential, now time.Time) ([]byte, requestOptions, error) {
	var request chatRequest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&request); err != nil {
		return nil, requestOptions{}, fmt.Errorf("invalid OpenAI chat request")
	}

	model := resolveModel(executorModel, request.Model, cred)
	if model == "" {
		return nil, requestOptions{}, fmt.Errorf("model is required")
	}
	if len(request.Messages) == 0 {
		return nil, requestOptions{}, fmt.Errorf("messages are required")
	}

	var system []string
	messages := make([]any, 0, len(request.Messages))
	for _, message := range request.Messages {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "developer", "system":
			text, err := textContent(message.Content)
			if err != nil {
				return nil, requestOptions{}, err
			}
			if text != "" {
				system = append(system, text)
			}
		case "user":
			blocks, err := textBlocks(message.Content)
			if err != nil {
				return nil, requestOptions{}, err
			}
			messages = append(messages, map[string]any{"role": "user", "content": blocks})
		case "assistant":
			blocks, err := textBlocksOptional(message.Content)
			if err != nil {
				return nil, requestOptions{}, err
			}
			for _, call := range message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" || call.Type != "function" || strings.TrimSpace(call.Function.Name) == "" {
					return nil, requestOptions{}, fmt.Errorf("assistant tool call is invalid")
				}
				input, errInput := parseToolArguments(call.Function.Arguments)
				if errInput != nil {
					return nil, requestOptions{}, errInput
				}
				blocks = append(blocks, map[string]any{
					"type":       "tool-call",
					"toolCallId": call.ID,
					"toolName":   call.Function.Name,
					"input":      input,
				})
			}
			if len(blocks) == 0 {
				return nil, requestOptions{}, fmt.Errorf("assistant message has no supported content")
			}
			messages = append(messages, map[string]any{"role": "assistant", "content": blocks})
		case "tool":
			if strings.TrimSpace(message.ToolCallID) == "" {
				return nil, requestOptions{}, fmt.Errorf("tool_call_id is required")
			}
			text, err := textContent(message.Content)
			if err != nil {
				return nil, requestOptions{}, err
			}
			messages = append(messages, map[string]any{
				"role": "tool",
				"content": []any{map[string]any{
					"type":       "tool-result",
					"toolCallId": message.ToolCallID,
					"toolName":   strings.TrimSpace(message.Name),
					"output":     map[string]any{"type": "text", "value": text},
				}},
			})
		default:
			return nil, requestOptions{}, fmt.Errorf("unsupported message role")
		}
	}
	if len(messages) == 0 {
		return nil, requestOptions{}, fmt.Errorf("messages contain no user, assistant, or tool content")
	}

	params := map[string]any{"model": model, "messages": messages, "stream": true}
	if len(system) > 0 {
		params["system"] = strings.Join(system, "\n\n")
	}
	if request.MaxCompletionTokens != nil {
		params["max_tokens"] = *request.MaxCompletionTokens
	} else if request.MaxTokens != nil {
		params["max_tokens"] = *request.MaxTokens
	}
	if request.Temperature != nil {
		params["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		params["top_p"] = *request.TopP
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		params["reasoning_effort"] = effort
	}
	if len(request.Tools) > 0 {
		tools := make([]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			if tool.Type != "function" || strings.TrimSpace(tool.Function.Name) == "" {
				return nil, requestOptions{}, fmt.Errorf("only function tools are supported")
			}
			var schema any = map[string]any{"type": "object"}
			if len(tool.Function.Parameters) > 0 {
				if !json.Valid(tool.Function.Parameters) || json.Unmarshal(tool.Function.Parameters, &schema) != nil {
					return nil, requestOptions{}, fmt.Errorf("tool parameters must be valid JSON Schema")
				}
			}
			tools = append(tools, map[string]any{
				"type":         "function",
				"name":         tool.Function.Name,
				"description":  tool.Function.Description,
				"input_schema": schema,
			})
		}
		params["tools"] = tools
	}

	body := map[string]any{
		"config": map[string]any{
			"workingDir": "", "date": now.UTC().Format("2006-01-02"),
			"environment": runtime.GOOS + "-" + runtime.GOARCH,
			"structure":   []any{}, "isGitRepo": false,
			"currentBranch": "", "mainBranch": "", "gitStatus": "", "recentCommits": []any{},
		},
		"memory": "", "taste": nil, "skills": nil,
		"permissionMode": permissionMode, "threadId": sessionID, "params": params,
	}
	translated, err := json.Marshal(body)
	if err != nil {
		return nil, requestOptions{}, fmt.Errorf("failed to encode upstream request")
	}
	return translated, requestOptions{Stream: request.Stream, IncludeUsage: request.StreamOptions.IncludeUsage}, nil
}

// resolveModel picks the upstream model id.
//
// Precedence: the model the host selected, then the client-requested model.
// Either may be a client-visible alias, which is mapped back to the upstream
// model name. An unknown model is passed through because execution is
// permissive by decision.
func resolveModel(executorModel, requested string, cred auth.Credential) string {
	candidate := strings.TrimSpace(executorModel)
	if candidate == "" {
		candidate = strings.TrimSpace(requested)
	}
	if candidate == "" {
		return ""
	}
	for _, selected := range cred.Models {
		name := strings.TrimSpace(selected.Name)
		alias := strings.TrimSpace(selected.Alias)
		if name == "" {
			continue
		}
		if alias != "" && alias == candidate {
			return name
		}
		if name == candidate {
			return candidate
		}
	}
	return candidate
}

func textContent(raw json.RawMessage) (string, error) {
	blocks, err := textBlocks(raw)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text, ok := block["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ""), nil
}

func textBlocksOptional(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return textBlocks(raw)
}

func textBlocks(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []map[string]any{{"type": "text", "text": ""}}, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []map[string]any{{"type": "text", "text": text}}, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("message content must be text")
	}
	blocks := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text", "input_text":
			blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
		default:
			return nil, fmt.Errorf("unsupported message content block")
		}
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("message content is empty")
	}
	return blocks, nil
}

func parseToolArguments(raw json.RawMessage) (map[string]any, error) {
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		raw = []byte(encoded)
	}
	var input map[string]any
	if len(raw) == 0 || !json.Valid(raw) || json.Unmarshal(raw, &input) != nil || input == nil {
		return nil, fmt.Errorf("assistant tool arguments must be a valid JSON object")
	}
	return input, nil
}

var (
	_ = http.MethodPost
	_ = streamObjectName
	_ = maxEventBytes
	_ = cliVersion
	_ = generatePath
)
