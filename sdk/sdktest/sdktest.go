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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/circleci-functions/toolkit/sdk"
)

// Server is an in-memory cache a function under test can save to and restore
// from. Its behaviour mirrors CircleCI's, including write-once keys and saves
// registered to run at the end of the job.
type Server struct {
	srv   *httptest.Server
	cache *cacheStore
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

	s := &Server{srv: httptest.NewServer(mux), cache: cs}
	t.Cleanup(s.srv.Close)
	return s
}

// PendingCacheSaves returns the keys of the cache saves registered to run at
// the end of the job and not yet run, in the order they were registered.
//
// Use it to assert a function registered a save without having to end the job
// first. A key already written is still reported here: the save was registered,
// and whether it turns out to be a no-op is only settled when the job ends.
func (s *Server) PendingCacheSaves() []string {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()

	keys := make([]string, 0, len(s.cache.pending))
	for _, p := range s.cache.pending {
		keys = append(keys, p.key)
	}
	return keys
}

// EndJob runs the work registered to run at the end of a successful job, in
// reverse order of registration, as the job itself would. Work registered with
// OnFail is discarded.
//
// Write-once keys apply here too, so of several saves registered under one key
// the first to run — the last registered — is the one that takes effect.
func (s *Server) EndJob(t *testing.T) {
	t.Helper()
	s.endJob(t, true)
}

// EndJobAfterFailure is EndJob for a job whose steps failed: work registered
// with OnSuccess is discarded instead.
func (s *Server) EndJobAfterFailure(t *testing.T) {
	t.Helper()
	s.endJob(t, false)
}

func (s *Server) endJob(t *testing.T, succeeded bool) {
	t.Helper()

	s.cache.mu.Lock()
	pending := s.cache.pending
	s.cache.pending = nil
	s.cache.mu.Unlock()

	for _, p := range slices.Backward(pending) {
		if !p.runsWhen(succeeded) {
			continue
		}
		if err := s.cache.save(p.key, p.paths); err != nil {
			t.Fatalf("save cache %q at end of job: %v", p.key, err)
		}
	}
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
	t.Setenv("CIRCLECI_PLUGIN_URL", s.srv.URL)
}

// SavedKeys returns the keys written to the cache so far, sorted. Useful for
// asserting a function partitions its cache keys the way you expect.
//
// Saves still registered for the end of the job are not written yet and so are
// not included; see PendingCacheSaves.
func (s *Server) SavedKeys() []string {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()

	keys := make([]string, 0, len(s.cache.entries))
	for k := range s.cache.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type cacheEntry struct {
	paths    []string
	storeDir string
	// seq orders entries by the order they were written, so a prefix match can
	// pick the most recent as CircleCI does.
	//
	// A counter rather than a timestamp: two saves in one test can land on the
	// same clock tick, and a tie would make which entry a prefix restore
	// returns nondeterministic.
	seq uint64
}

// pendingSave is a cache save registered to run at the end of the job.
type pendingSave struct {
	key   string
	paths []string
	when  string
}

// runsWhen reports whether this registration runs given the job's outcome.
func (p pendingSave) runsWhen(jobSucceeded bool) bool {
	switch p.when {
	case "on_success":
		return jobSucceeded
	case "on_fail":
		return !jobSucceeded
	default:
		return true
	}
}

type cacheStore struct {
	mu          sync.Mutex
	entries     map[string]*cacheEntry
	storeDir    string
	pending     []pendingSave
	mutableKeys bool
	// seq counts saves, to order entries. See cacheEntry.seq.
	seq uint64
}

func (cs *cacheStore) handleSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key      string   `json:"key"`
		Paths    []string `json:"paths"`
		Teardown bool     `json:"teardown"`
		When     string   `json:"when"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Teardown {
		cs.mu.Lock()
		cs.pending = append(cs.pending, pendingSave{key: req.Key, paths: req.Paths, when: req.When})
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

// save copies paths into the store under key. A key already written is left
// alone: CircleCI cache keys are write-once, and a save under an existing key
// is skipped rather than failing.
func (cs *cacheStore) save(key string, paths []string) error {
	cs.mu.Lock()
	_, exists := cs.entries[key]
	mutable := cs.mutableKeys
	cs.mu.Unlock()
	if exists && !mutable {
		return nil
	}

	keyDir := filepath.Join(cs.storeDir, keyHash(key))
	// Indexed by position rather than basename, so two saved paths sharing a
	// basename stay distinct.
	for i, p := range paths {
		if err := copyDirTree(p, filepath.Join(keyDir, strconv.Itoa(i))); err != nil {
			return err
		}
	}

	cs.mu.Lock()
	cs.seq++
	cs.entries[key] = &cacheEntry{paths: paths, storeDir: keyDir, seq: cs.seq}
	cs.mu.Unlock()
	return nil
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
		e := cs.match(key)
		if e == nil {
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
	writeResult(w, "success")
}

// match returns the entry a restore of key gets, or nil for a miss.
//
// CircleCI matches a restore key as a prefix and takes the most recently saved
// entry among those matching, so a function can offer a list of keys that
// broadens as it goes — an exact key first, then shorter prefixes — and
// warm-start from a near miss instead of nothing. Callers rely on that, so an
// exact-match-only fake would report a miss where a job restores an older
// entry.
//
// The caller holds cs.mu.
func (cs *cacheStore) match(key string) *cacheEntry {
	var best *cacheEntry
	for k, e := range cs.entries {
		if !strings.HasPrefix(k, key) {
			continue
		}
		if best == nil || e.seq > best.seq {
			best = e
		}
	}
	return best
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
