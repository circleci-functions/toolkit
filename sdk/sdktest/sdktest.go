// Package sdktest provides a fake CircleCI plugin server for use in tests.
package sdktest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/circleci-functions/toolkit/sdk"
)

// Server is a fake CircleCI plugin server for use in tests.
type Server struct {
	srv *httptest.Server
}

// NewServer starts a fake plugin server and registers cleanup with t.
func NewServer(t *testing.T) *Server {
	t.Helper()

	cs := &cacheStore{
		entries:  make(map[string]*cacheEntry),
		storeDir: t.TempDir(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/step/cache/save", cs.handleSave)
	mux.HandleFunc("/step/cache/restore", cs.handleRestore)

	s := &Server{srv: httptest.NewServer(mux)}
	t.Cleanup(s.srv.Close)
	return s
}

// Client returns an sdk.Client configured to talk to this server.
func (s *Server) Client() *sdk.Client {
	return sdk.NewWithBaseURL(s.srv.URL)
}

// SetupEnv sets CIRCLECI_PLUGIN_URL in the test environment so that subprocess
// binaries started by this test connect to this server. The env var is
// automatically cleared when the test ends.
func (s *Server) SetupEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CIRCLECI_PLUGIN_URL", s.srv.URL)
}

type cacheEntry struct {
	paths    []string
	storeDir string
}

type cacheStore struct {
	mu       sync.Mutex
	entries  map[string]*cacheEntry
	storeDir string
}

func (cs *cacheStore) handleSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string   `json:"key"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h := sha256.New()
	_, _ = io.WriteString(h, req.Key)
	keyDir := filepath.Join(cs.storeDir, hex.EncodeToString(h.Sum(nil))[:16])
	for _, p := range req.Paths {
		if err := copyDirTree(p, filepath.Join(keyDir, filepath.Base(p))); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	cs.mu.Lock()
	cs.entries[req.Key] = &cacheEntry{paths: req.Paths, storeDir: keyDir}
	cs.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Result string `json:"result"`
	}{"success"})
}

func (cs *cacheStore) handleRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keys []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, key := range req.Keys {
		e, ok := cs.entries[key]
		if !ok {
			continue
		}
		for _, p := range e.paths {
			if err := copyDirTree(filepath.Join(e.storeDir, filepath.Base(p)), p); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Result string `json:"result"`
		}{"success"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Result string `json:"result"`
	}{"success"})
}

func copyDirTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
