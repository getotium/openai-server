package server

import (
	"context"
	"encoding/json"

	"github.com/getotium/openai"
)

// Runner turns one parsed batch request into its output line. It is the pluggable seam of the
// reference server: supply your own to route requests at a real inference backend (vLLM, an
// upstream OpenAI-compatible API, a local model…).
//
// A Runner should NOT return a transport error for a per-request failure — set the output's Error
// field and return nil, so one bad request doesn't fail the whole batch. A returned error is
// treated as a batch-level fault (the batch fails).
type Runner func(ctx context.Context, req openai.ParsedRequest) (openai.BatchRequestOutput, error)

// EchoRunner is a trivial reference Runner: it returns a well-formed chat-completion response that
// echoes the request. It has no dependencies and does no inference — it exists so the server (and
// the conformance suite) works out of the box, and as the template for a real Runner.
func EchoRunner(_ context.Context, req openai.ParsedRequest) (openai.BatchRequestOutput, error) {
	body, err := json.Marshal(map[string]any{
		"id":      "chatcmpl-" + req.CustomID,
		"object":  "chat.completion",
		"created": 0,
		"model":   req.Model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "echo"},
				"finish_reason": "stop",
			},
		},
	})
	if err != nil {
		return openai.BatchRequestOutput{}, err
	}
	return openai.BatchRequestOutput{
		ID:       "batch_req_" + req.CustomID,
		CustomID: req.CustomID,
		Response: &openai.OutputResponse{StatusCode: 200, RequestID: "req_" + req.CustomID, Body: body},
	}, nil
}
