package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getotium/openai"
	server "github.com/getotium/openai-server"
)

const batchInput = `{"custom_id":"a","method":"POST","url":"/v1/chat/completions","body":{"model":"m","messages":[{"role":"user","content":"hi"}]}}
{"custom_id":"b","method":"POST","url":"/v1/chat/completions","body":{"model":"m","messages":[{"role":"user","content":"yo"}]}}
`

// The whole batch round-trip against the reference server (echo runner), over real HTTP: upload
// input, create a batch, poll to completion, download and verify the output file.
func TestBatchRoundTrip(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(server.New(server.EchoRunner).Handler())
	defer ts.Close()

	// Upload the input file.
	inFile := uploadFile(t, ts.URL, batchInput)
	if inFile.ID == "" || inFile.Purpose != openai.PurposeBatch {
		t.Fatalf("bad uploaded file: %+v", inFile)
	}

	// Create the batch.
	var batch openai.Batch
	postJSON(t, ts.URL+"/v1/batches", map[string]any{
		"input_file_id":     inFile.ID,
		"endpoint":          "/v1/chat/completions",
		"completion_window": "24h",
	}, &batch)
	if batch.ID == "" || batch.Status != openai.BatchValidating {
		t.Fatalf("expected a validating batch, got %+v", batch)
	}

	// Poll to a terminal state.
	final := poll(t, ts.URL+"/v1/batches/"+batch.ID)
	if final.Status != openai.BatchCompleted {
		t.Fatalf("batch did not complete: status=%s errors=%+v", final.Status, final.Errors)
	}
	if final.RequestCounts.Total != 2 || final.RequestCounts.Completed != 2 {
		t.Fatalf("counts = %+v, want total=2 completed=2", final.RequestCounts)
	}
	if final.OutputFileID == "" {
		t.Fatal("completed batch has no output_file_id")
	}

	// Download + parse the output file.
	out := getContent(t, ts.URL+"/v1/files/"+final.OutputFileID+"/content")
	byCustom := map[string]openai.BatchRequestOutput{}
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		var o openai.BatchRequestOutput
		if err := json.Unmarshal(line, &o); err != nil {
			t.Fatalf("bad output line %q: %v", line, err)
		}
		byCustom[o.CustomID] = o
	}
	for _, id := range []string{"a", "b"} {
		o, ok := byCustom[id]
		if !ok {
			t.Fatalf("missing output for custom_id %q", id)
		}
		if o.Error != nil || o.Response == nil || o.Response.StatusCode != 200 {
			t.Fatalf("custom_id %q: unexpected output %+v", id, o)
		}
	}
}

func TestCreateBatch_UnknownInputFile(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(server.New(server.EchoRunner).Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/batches",
		bytes.NewBufferString(`{"input_file_id":"file-nope"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// --- helpers ---

func uploadFile(t *testing.T, base, content string) openai.FileObject {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("purpose", "batch")
	fw, err := mw.CreateFormFile("file", "input.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}
	var f openai.FileObject
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

func postJSON(t *testing.T, url string, body, dst any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatal(err)
	}
}

func poll(t *testing.T, url string) openai.Batch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var b openai.Batch
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(resp.Body).Decode(&b)
		_ = resp.Body.Close()
		switch b.Status {
		case openai.BatchCompleted, openai.BatchFailed, openai.BatchCancelled, openai.BatchExpired:
			return b
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("batch did not reach a terminal state in time")
	return openai.Batch{}
}

func getContent(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", url, resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	return data
}
