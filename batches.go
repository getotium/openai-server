package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/getotium/openai"
)

type createBatchReq struct {
	InputFileID      string            `json:"input_file_id"`
	Endpoint         string            `json:"endpoint"`
	CompletionWindow string            `json:"completion_window"`
	Metadata         map[string]string `json:"metadata"`
}

// createBatch handles POST /v1/batches: validate the request, create the batch in `validating`,
// and kick off processing. Returns the batch immediately (the client polls GET for completion).
func (s *Server) createBatch(w http.ResponseWriter, r *http.Request) {
	var req createBatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body", openai.ErrorTypeInvalidRequest)
		return
	}
	if req.InputFileID == "" {
		writeError(w, http.StatusBadRequest, "input_file_id is required", openai.ErrorTypeInvalidRequest)
		return
	}
	endpoint := req.Endpoint
	if endpoint == "" {
		endpoint = openai.EndpointChatCompletions
	}
	window := req.CompletionWindow
	if window == "" {
		window = openai.CompletionWindow24h
	}

	s.mu.Lock()
	if _, ok := s.files[req.InputFileID]; !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "no such input file", openai.ErrorTypeNotFound)
		return
	}
	b := &openai.Batch{
		ID:               newID("batch"),
		Object:           openai.ObjectBatch,
		Endpoint:         endpoint,
		InputFileID:      req.InputFileID,
		CompletionWindow: window,
		Status:           openai.BatchValidating,
		CreatedAt:        s.now().Unix(),
		Metadata:         req.Metadata,
	}
	s.batches[b.ID] = b
	snap := *b
	s.mu.Unlock()

	go s.process(b.ID)
	writeJSON(w, http.StatusOK, snap)
}

// getBatch handles GET /v1/batches/{id}. Returns a snapshot so the caller never sees the batch
// mutate under it while processing advances.
func (s *Server) getBatch(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	b, ok := s.batches[r.PathValue("id")]
	var snap openai.Batch
	if ok {
		snap = *b
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no such batch", openai.ErrorTypeNotFound)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// listBatches handles GET /v1/batches (single page).
func (s *Server) listBatches(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := make([]openai.Batch, 0, len(s.batches))
	for _, b := range s.batches {
		out = append(out, *b)
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, openai.NewList(out))
}

// cancelBatch handles POST /v1/batches/{id}/cancel. Best-effort: a terminal batch is returned as-is.
func (s *Server) cancelBatch(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	b, ok := s.batches[r.PathValue("id")]
	if ok && !isTerminal(b.Status) {
		now := s.now().Unix()
		b.Status = openai.BatchCancelled
		b.CancelledAt = &now
	}
	var snap openai.Batch
	if ok {
		snap = *b
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no such batch", openai.ErrorTypeNotFound)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func isTerminal(status string) bool {
	switch status {
	case openai.BatchCompleted, openai.BatchFailed, openai.BatchCancelled, openai.BatchExpired:
		return true
	}
	return false
}

// process runs a batch to completion: parse the input file, run each request through the Runner,
// write the output file, and finalize. Runs in its own goroutine; all shared-state access is under
// the mutex.
func (s *Server) process(id string) {
	s.mu.Lock()
	b, ok := s.batches[id]
	if !ok || b.Status != openai.BatchValidating {
		s.mu.Unlock()
		return
	}
	sf, haveInput := s.files[b.InputFileID]
	endpoint := b.Endpoint
	now := s.now().Unix()
	b.Status = openai.BatchInProgress
	b.InProgressAt = &now
	inputData := sf.data
	s.mu.Unlock()

	if !haveInput {
		s.failBatch(id, "input file not found", nil)
		return
	}

	reqs, lineErrs := openai.ParseRequests(bytes.NewReader(inputData), endpoint)
	if len(lineErrs) > 0 {
		errs := make([]openai.BatchError, len(lineErrs))
		for i, le := range lineErrs {
			line := le.Line
			errs[i] = openai.BatchError{Code: "invalid_line", Message: le.Message, Line: &line}
		}
		s.failBatch(id, "", errs)
		return
	}

	counts := openai.RequestCounts{Total: len(reqs)}
	outputs := make([]openai.BatchRequestOutput, 0, len(reqs))
	for _, rq := range reqs {
		out, err := s.runner(context.Background(), rq)
		if err != nil {
			s.failBatch(id, "runner error: "+err.Error(), nil)
			return
		}
		if out.Error != nil {
			counts.Failed++
		} else {
			counts.Completed++
		}
		outputs = append(outputs, out)
	}

	var buf bytes.Buffer
	if err := openai.WriteOutputs(&buf, outputs); err != nil {
		s.failBatch(id, "encode outputs: "+err.Error(), nil)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok = s.batches[id]
	if !ok || b.Status != openai.BatchInProgress {
		return // cancelled or gone while we worked
	}
	outID := newID("file")
	s.files[outID] = storedFile{
		obj: openai.FileObject{
			ID:        outID,
			Object:    openai.ObjectFile,
			Bytes:     int64(buf.Len()),
			CreatedAt: s.now().Unix(),
			Filename:  id + "_output.jsonl",
			Purpose:   openai.PurposeBatchOutput,
		},
		data: buf.Bytes(),
	}
	fin := s.now().Unix()
	b.OutputFileID = outID
	b.RequestCounts = counts
	b.Status = openai.BatchCompleted
	b.CompletedAt = &fin
}

// failBatch marks a batch failed, optionally attaching a validation error list.
func (s *Server) failBatch(id, msg string, errs []openai.BatchError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.batches[id]
	if !ok || isTerminal(b.Status) {
		return
	}
	now := s.now().Unix()
	b.Status = openai.BatchFailed
	b.FailedAt = &now
	if len(errs) == 0 && msg != "" {
		errs = []openai.BatchError{{Code: "processing_error", Message: msg}}
	}
	if len(errs) > 0 {
		b.Errors = &openai.ErrorList{Object: openai.ObjectList, Data: errs}
	}
}
