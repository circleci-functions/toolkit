package sdktest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type outputStore struct {
	mu     sync.Mutex
	values map[string]any
}

// Outputs returns the outputs set so far, by name. A value has the Go type it
// was set with: a string, bool, int or []string.
//
// The server doesn't know the function's function.yaml, so unlike a job it
// accepts a name the function never declared. Assert on the names here to check
// they match the declaration.
func (s *Server) Outputs() map[string]any {
	s.outputs.mu.Lock()
	defer s.outputs.mu.Unlock()

	out := make(map[string]any, len(s.outputs.values))
	for name, v := range s.outputs.values {
		if list, ok := v.([]string); ok {
			v = append([]string{}, list...)
		}
		out[name] = v
	}
	return out
}

func (o *outputStore) handleSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	value, err := decodeOutput(req.Name, req.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	o.mu.Lock()
	o.values[req.Name] = value
	o.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

// decodeOutput turns raw into the Go type SetOutput took it as, refusing what
// the build agent refuses: anything but a string, bool, integer or list of
// strings, and a NUL byte, which an environment variable can't hold.
func decodeOutput(name string, raw json.RawMessage) (any, error) {
	unsupported := fmt.Errorf("output '%s' must be a string, bool, int or list", name)
	nul := fmt.Errorf("output '%s' contains a NUL byte", name)

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, unsupported
	}

	switch v := v.(type) {
	case string:
		if strings.ContainsRune(v, 0) {
			return nil, nul
		}
		return v, nil
	case bool:
		return v, nil
	case json.Number:
		i, err := strconv.Atoi(v.String())
		if err != nil {
			return nil, unsupported
		}
		return i, nil
	case []any:
		list := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, unsupported
			}
			if strings.ContainsRune(s, 0) {
				return nil, nul
			}
			list = append(list, s)
		}
		return list, nil
	default:
		return nil, unsupported
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{msg})
}
