// Package sdktest provides an in-memory stand-in for the privileged operations
// sdk performs, plus a Sandbox that keeps a function under test from writing
// outside temp directories.
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
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/circleci-functions/toolkit/sdk"
)

// Server is an in-memory cache a function under test can save to and restore
// from. Its behaviour mirrors CircleCI's, including write-once keys.
type Server struct {
	srv   *httptest.Server
	store *cacheStore
}

// Option configures a Server.
type Option func(*cacheStore)

// WithMutableKeys lets a save overwrite an existing key.
//
// The default is to skip the save, matching CircleCI: cache keys are
// write-once, so the first save under a key wins and later saves under the
// same key are no-ops. Only use this if your test deliberately needs the
// unrealistic behaviour.
func WithMutableKeys() Option {
	return func(cs *cacheStore) {
		cs.mutableKeys = true
	}
}

// NewServer starts an in-memory cache and registers cleanup with t.
func NewServer(t *testing.T, opts ...Option) *Server {
	t.Helper()

	cs := &cacheStore{
		entries:  make(map[string]*cacheEntry),
		storeDir: t.TempDir(),
	}
	for _, opt := range opts {
		opt(cs)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/step/cache/save", cs.handleSave)
	mux.HandleFunc("/step/cache/restore", cs.handleRestore)

	s := &Server{srv: httptest.NewServer(mux), store: cs}
	t.Cleanup(s.srv.Close)
	return s
}

// Client returns an sdk.Client that performs its cache operations here.
func (s *Server) Client() *sdk.Client {
	return sdk.NewWithBaseURL(s.srv.URL)
}

// URL returns the base URL this server is listening on.
func (s *Server) URL() string {
	return s.srv.URL
}

// SetupEnv points the test environment at this server, so subprocess binaries
// started by the test use it for cache operations. Undone when the test ends.
// Prefer NewSandbox, which does this and isolates the filesystem too.
func (s *Server) SetupEnv(t *testing.T) {
	t.Helper()
	t.Setenv(sdk.PluginURLEnvVar, s.srv.URL)
}

// SavedKeys returns the keys written to the cache so far, sorted. Useful for
// asserting a function partitions its cache keys the way you expect.
func (s *Server) SavedKeys() []string {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()

	keys := make([]string, 0, len(s.store.entries))
	for k := range s.store.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type cacheEntry struct {
	paths    []string
	storeDir string
}

type cacheStore struct {
	mu          sync.Mutex
	entries     map[string]*cacheEntry
	storeDir    string
	mutableKeys bool
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

	cs.mu.Lock()
	_, exists := cs.entries[req.Key]
	cs.mu.Unlock()
	if exists && !cs.mutableKeys {
		writeSuccess(w)
		return
	}

	keyDir := filepath.Join(cs.storeDir, keyHash(req.Key))
	for i, p := range req.Paths {
		if err := copyDirTree(p, filepath.Join(keyDir, strconv.Itoa(i))); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	cs.mu.Lock()
	cs.entries[req.Key] = &cacheEntry{paths: req.Paths, storeDir: keyDir}
	cs.mu.Unlock()
	writeSuccess(w)
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
		for i, p := range e.paths {
			if err := copyDirTree(filepath.Join(e.storeDir, strconv.Itoa(i)), p); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		break
	}
	writeSuccess(w)
}

func writeSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Result string `json:"result"`
	}{"success"})
}

func keyHash(key string) string {
	h := sha256.New()
	_, _ = io.WriteString(h, key)
	return hex.EncodeToString(h.Sum(nil))[:16]
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
