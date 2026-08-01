package server

import (
	"io"
	"net/http"

	"github.com/getotium/openai"
)

// maxUploadBytes bounds a single file upload (generous for a reference server).
const maxUploadBytes = 512 << 20 // 512 MiB

// createFile handles POST /v1/files — a multipart upload with fields `file` and `purpose`. It
// stores the bytes and returns the OpenAI FileObject an SDK expects.
func (s *Server) createFile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse multipart form: "+err.Error(), openai.ErrorTypeInvalidRequest)
		return
	}
	purpose := r.FormValue("purpose")
	if purpose == "" {
		purpose = openai.PurposeBatch
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing form field \"file\"", openai.ErrorTypeInvalidRequest)
		return
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read file: "+err.Error(), openai.ErrorTypeInvalidRequest)
		return
	}

	obj := openai.FileObject{
		ID:        newID("file"),
		Object:    openai.ObjectFile,
		Bytes:     int64(len(data)),
		CreatedAt: s.now().Unix(),
		Filename:  hdr.Filename,
		Purpose:   purpose,
	}
	s.mu.Lock()
	s.files[obj.ID] = storedFile{obj: obj, data: data}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, obj)
}

// getFile handles GET /v1/files/{id} — the file's metadata.
func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sf, ok := s.files[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no such file", openai.ErrorTypeNotFound)
		return
	}
	writeJSON(w, http.StatusOK, sf.obj)
}

// getFileContent handles GET /v1/files/{id}/content — the raw file bytes (JSONL).
func (s *Server) getFileContent(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sf, ok := s.files[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no such file", openai.ErrorTypeNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/jsonl")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(sf.data)
}
