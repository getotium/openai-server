package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/getotium/openai"
	server "github.com/getotium/openai-server"
)

// TestConformance_OpenAISDK drives the reference server with the OFFICIAL openai-go SDK — the real
// "a stock OpenAI client works by base-URL swap" proof. It runs the full batch flow through the SDK:
// upload the input file, create the batch, poll it to completion, and download the output — asserting
// the round-trip the same way a customer integrating against Otium would experience it.
func TestConformance_OpenAISDK(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(server.New(server.EchoRunner).Handler())
	defer ts.Close()

	// The only integration step: point the SDK at our base URL. The API key is required by the SDK
	// but ignored by the reference server.
	client := oai.NewClient(
		option.WithBaseURL(ts.URL+"/v1/"),
		option.WithAPIKey("test-key"),
	)
	ctx := context.Background()

	// Upload the batch input file.
	f, err := client.Files.New(ctx, oai.FileNewParams{
		File:    oai.File(bytes.NewReader([]byte(batchInput)), "input.jsonl", "application/jsonl"),
		Purpose: oai.FilePurposeBatch,
	})
	if err != nil {
		t.Fatalf("Files.New: %v", err)
	}
	if f.ID == "" {
		t.Fatal("SDK got no file id")
	}

	// Create the batch.
	b, err := client.Batches.New(ctx, oai.BatchNewParams{
		InputFileID:      f.ID,
		Endpoint:         oai.BatchNewParamsEndpointV1ChatCompletions,
		CompletionWindow: oai.BatchNewParamsCompletionWindow24h,
	})
	if err != nil {
		t.Fatalf("Batches.New: %v", err)
	}

	// Poll to a terminal state via the SDK.
	var final *oai.Batch
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := client.Batches.Get(ctx, b.ID)
		if err != nil {
			t.Fatalf("Batches.Get: %v", err)
		}
		if got.Status == oai.BatchStatusCompleted || got.Status == oai.BatchStatusFailed {
			final = got
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final == nil {
		t.Fatal("batch did not reach a terminal state in time (via SDK)")
	}
	if final.Status != oai.BatchStatusCompleted {
		t.Fatalf("batch failed via SDK: status=%s", final.Status)
	}
	if final.OutputFileID == "" {
		t.Fatal("completed batch has no output_file_id")
	}

	// Download the output file via the SDK and verify both requests round-tripped.
	resp, err := client.Files.Content(ctx, final.OutputFileID)
	if err != nil {
		t.Fatalf("Files.Content: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)

	seen := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var o openai.BatchRequestOutput
		if err := json.Unmarshal(line, &o); err != nil {
			t.Fatalf("bad output line %q: %v", line, err)
		}
		if o.Response == nil || o.Response.StatusCode != 200 {
			t.Fatalf("custom_id %q: unexpected output %+v", o.CustomID, o)
		}
		seen[o.CustomID] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("SDK round-trip missing outputs: %v", seen)
	}
}
