// Package server is a small, in-memory reference implementation of the OpenAI-compatible Files +
// Batch API. It speaks the exact wire shapes (via github.com/getotium/openai) so a base-URL-swapped
// OpenAI client works against it, and runs each batch request through a pluggable Runner.
//
// It is a conformance harness and a starting point — everything lives in memory, there is no auth,
// and processing is synchronous-on-a-goroutine. Swap the Runner (and the storage, if you fork it)
// to point it at a real backend. What's open is the harness and the contract; what you plug in is
// yours.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/getotium/openai"
)

// Server is an in-memory OpenAI-compatible Files + Batch API server.
type Server struct {
	runner Runner
	now    func() time.Time

	mu      sync.Mutex
	files   map[string]storedFile
	batches map[string]*openai.Batch
}

type storedFile struct {
	obj  openai.FileObject
	data []byte
}

// New builds a Server that runs each batch request through runner. Pass EchoRunner for a
// no-backend demo.
func New(runner Runner) *Server {
	return &Server{
		runner:  runner,
		now:     time.Now,
		files:   map[string]storedFile{},
		batches: map[string]*openai.Batch{},
	}
}

// Handler returns the HTTP handler implementing the /v1 Files + Batch endpoints. Uses net/http's
// method+pattern routing (Go 1.22+), so no router dependency.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/files", s.createFile)
	mux.HandleFunc("GET /v1/files/{id}", s.getFile)
	mux.HandleFunc("GET /v1/files/{id}/content", s.getFileContent)
	mux.HandleFunc("POST /v1/batches", s.createBatch)
	mux.HandleFunc("GET /v1/batches", s.listBatches)
	mux.HandleFunc("GET /v1/batches/{id}", s.getBatch)
	mux.HandleFunc("POST /v1/batches/{id}/cancel", s.cancelBatch)
	return mux
}

// newID mints an OpenAI-style prefixed id (e.g. "file-9f8c…", "batch-…").
func newID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg, typ string) {
	writeJSON(w, code, openai.NewError(msg, typ))
}
