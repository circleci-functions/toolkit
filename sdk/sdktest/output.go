package sdktest

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"sync"
)

// Outputs returns the outputs set so far, by name. Values have the Go type
// they were set with: string, bool, int or []string.
//
// The server does not know the function's function.yaml, so unlike a job it
// accepts any name. Assert on the names here to check they match what the
// function declares.
func (s *Server) Outputs() map[string]any {
	s.outputs.mu.Lock()
	defer s.outputs.mu.Unlock()
	return maps.Clone(s.outputs.values)
}

type outputStore struct {
	mu     sync.Mutex
	values map[string]any
}

func (o *outputStore) handleSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	value, err := decodeOutput(req.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "output '"+req.Name+"': "+err.Error())
		return
	}

	o.mu.Lock()
	o.values[req.Name] = value
	o.mu.Unlock()
	writeResult(w, "success")
}

// decodeOutput turns a JSON value back into the Go type sdk.SetOutput accepts.
func decodeOutput(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, errors.New("value is missing")
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b, nil
	}
	var i int
	if json.Unmarshal(raw, &i) == nil {
		return i, nil
	}
	var l []string
	if json.Unmarshal(raw, &l) == nil {
		return l, nil
	}
	return nil, errors.New("value must be a string, bool, int or list of strings")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{msg})
}
