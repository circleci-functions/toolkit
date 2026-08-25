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
	"slices"
	"sync"
	"testing"

	"github.com/circleci-functions/toolkit/sdk"
)

// Server is a fake CircleCI plugin server for use in tests.
type Server struct {
	srv   *httptest.Server
	cache *cacheStore
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

	s := &Server{srv: httptest.NewServer(mux), cache: cs}
	t.Cleanup(s.srv.Close)
	return s
}

// PendingCacheSaves returns the keys of the cache saves registered to run at
// the end of the job and not yet run, in the order they were registered.
//
// Use it to assert a function registered a save without having to end the job
// first.
func (s *Server) PendingCacheSaves() []string {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()

	keys := make([]string, 0, len(s.cache.pending))
	for _, p := range s.cache.pending {
		keys = append(keys, p.key)
	}
	return keys
}

// EndJob runs the work registered to run at the end of the job, in reverse
// order of registration, as the job itself would.
func (s *Server) EndJob(t *testing.T) {
	t.Helper()

	s.cache.mu.Lock()
	pending := s.cache.pending
	s.cache.pending = nil
	s.cache.mu.Unlock()

	for _, p := range slices.Backward(pending) {
		if err := s.cache.save(p.key, p.paths); err != nil {
			t.Fatalf("save cache %q at end of job: %v", p.key, err)
		}
	}
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

// pendingSave is a cache save registered to run at the end of the job.
type pendingSave struct {
	key   string
	paths []string
}

type cacheStore struct {
	mu       sync.Mutex
	entries  map[string]*cacheEntry
	storeDir string
	pending  []pendingSave
}

func (cs *cacheStore) handleSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key      string   `json:"key"`
		Paths    []string `json:"paths"`
		Teardown bool     `json:"teardown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Teardown {
		cs.mu.Lock()
		cs.pending = append(cs.pending, pendingSave{key: req.Key, paths: req.Paths})
		cs.mu.Unlock()
		writeDeferred(w)
		return
	}

	if err := cs.save(req.Key, req.Paths); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeResult(w, "success")
}

// save copies paths into the store under key.
func (cs *cacheStore) save(key string, paths []string) error {
	h := sha256.New()
	_, _ = io.WriteString(h, key)
	keyDir := filepath.Join(cs.storeDir, hex.EncodeToString(h.Sum(nil))[:16])
	for _, p := range paths {
		if err := copyDirTree(p, filepath.Join(keyDir, filepath.Base(p))); err != nil {
			return err
		}
	}
	cs.mu.Lock()
	cs.entries[key] = &cacheEntry{paths: paths, storeDir: keyDir}
	cs.mu.Unlock()
	return nil
}

func writeResult(w http.ResponseWriter, result string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Result string `json:"result"`
	}{result})
}

// writeDeferred matches the agent: 202, and no result, since the step has not run.
func writeDeferred(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(struct {
		Deferred bool `json:"deferred"`
	}{true})
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
