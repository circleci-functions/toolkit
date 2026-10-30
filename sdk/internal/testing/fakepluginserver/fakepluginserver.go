// Package fakepluginserver provides a stub of the server pkg/sdk talks to,
// for use in its own tests.
package fakepluginserver

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const socketName = "circleci-plugin.sock"

// Server is a running fake plugin server bound to a Unix domain socket,
// routing the same paths the real plugin server exposes.
type Server struct {
	// Dir is the directory containing the socket; pass it as
	// sdk.Config.SocketDir to point a Client at this server.
	Dir string

	mux *http.ServeMux
}

// New starts a Server on a Unix domain socket in a fresh temp directory.
// Use SaveCache and RestoreCache to configure how it responds to those
// routes. The server is stopped on test cleanup.
func New(t *testing.T) *Server {
	t.Helper()
	dir := Dir(t)

	listener, err := net.Listen("unix", SocketPath(dir))
	if err != nil {
		t.Fatalf("listen on socket: %v", err)
	}

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		_ = srv.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
	})

	return &Server{Dir: dir, mux: mux}
}

// SaveCache serves handler for the /step/cache/save route.
func (s *Server) SaveCache(handler http.Handler) {
	s.mux.Handle("/step/cache/save", handler)
}

// RestoreCache serves handler for the /step/cache/restore route.
func (s *Server) RestoreCache(handler http.Handler) {
	s.mux.Handle("/step/cache/restore", handler)
}

// StoreArtifacts serves handler for the /step/artifacts/store route.
func (s *Server) StoreArtifacts(handler http.Handler) {
	s.mux.Handle("/step/artifacts/store", handler)
}

// StoreTestResults serves handler for the /step/test-results/store route.
func (s *Server) StoreTestResults(handler http.Handler) {
	s.mux.Handle("/step/test-results/store", handler)
}

// Dir returns a fresh temp directory suitable for a socket, without
// starting a server — for tests exercising sdk's "no server running"
// fallback.
//
// It returns a short, flat directory rather than t.TempDir() directly:
// t.TempDir() nests the test name into the path, which routinely exceeds
// the ~104 byte sun_path limit on macOS.
func Dir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fakepluginserver")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

// SocketPath returns the path within dir where New (and a real plugin
// server) create their socket.
func SocketPath(dir string) string {
	return filepath.Join(dir, socketName)
}

// WriteStaleSocket creates a Unix domain socket file at SocketPath(dir)
// with nothing listening on it, simulating a server that exited without
// cleaning up after itself.
func WriteStaleSocket(t *testing.T, dir string) {
	t.Helper()
	listener, err := net.Listen("unix", SocketPath(dir))
	if err != nil {
		t.Fatalf("listen on socket: %v", err)
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		t.Fatalf("listener is not a *net.UnixListener")
	}
	unixListener.SetUnlinkOnClose(false)
	if err := unixListener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
}

// JSONHandler returns a handler that responds with status and encodes body
// as JSON.
func JSONHandler(t *testing.T, status int, body any) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode response: %v", err)
		}
	})
}
