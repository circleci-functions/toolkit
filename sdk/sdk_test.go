package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/internal/testing/fakepluginserver"
)

func TestSaveCache_Success(t *testing.T) {
	srv := fakepluginserver.New(t)
	srv.SaveCache(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key   string   `json:"key"`
			Paths []string `json:"paths"`
		}
		assert.NilError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, req.Key, "my-key")
		assert.DeepEqual(t, req.Paths, []string{"./cache"})

		assert.NilError(t, json.NewEncoder(w).Encode(map[string]string{"result": "success"}))
	}))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	err := client.SaveCache(context.Background(), "my-key", []string{"./cache"})
	assert.NilError(t, err)
}

func TestRestoreCache_Success(t *testing.T) {
	srv := fakepluginserver.New(t)
	srv.RestoreCache(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Keys []string `json:"keys"`
		}
		assert.NilError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.DeepEqual(t, req.Keys, []string{"my-key"})

		assert.NilError(t, json.NewEncoder(w).Encode(map[string]string{"result": "success"}))
	}))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	result, err := client.RestoreCache(context.Background(), []string{"my-key"})
	assert.NilError(t, err)
	assert.Equal(t, result.Raw, "success")
}

func TestSaveCache_ServerError(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "bad request", status: http.StatusBadRequest},
		{name: "internal error", status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakepluginserver.New(t)
			srv.SaveCache(fakepluginserver.JSONHandler(t, tt.status, map[string]string{"error": "boom"}))
			client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

			err := client.SaveCache(context.Background(), "k", []string{"."})
			assert.Assert(t, err != nil)

			var svrErr *ServerError
			assert.Assert(t, errors.As(err, &svrErr))
			assert.Equal(t, svrErr.Msg, "boom")
		})
	}
}

func TestErrNoServer(t *testing.T) {
	tests := []struct {
		name            string
		setup           func(t *testing.T) string // returns a socket dir
		wantErrNoServer bool
	}{
		{
			name:            "no socket file",
			setup:           fakepluginserver.Dir,
			wantErrNoServer: true,
		},
		{
			name: "plain file at socket path",
			setup: func(t *testing.T) string {
				dir := fakepluginserver.Dir(t)
				err := os.WriteFile(fakepluginserver.SocketPath(dir), []byte("not a socket"), 0o600)
				assert.NilError(t, err)
				return dir
			},
			wantErrNoServer: false,
		},
		{
			name: "stale socket",
			setup: func(t *testing.T) string {
				dir := fakepluginserver.Dir(t)
				fakepluginserver.WriteStaleSocket(t, dir)
				return dir
			},
			wantErrNoServer: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setup(t)
			client := newWithSocketPath(fakepluginserver.SocketPath(dir))

			err := client.SaveCache(context.Background(), "k", []string{"."})
			assert.Assert(t, err != nil)
			assert.Equal(t, errors.Is(err, ErrNoServer), tt.wantErrNoServer)
		})
	}

	t.Run("no socket file - RestoreCache", func(t *testing.T) {
		client := newWithSocketPath(fakepluginserver.SocketPath(fakepluginserver.Dir(t)))
		_, err := client.RestoreCache(context.Background(), []string{"k"})
		assert.Assert(t, errors.Is(err, ErrNoServer))
	})
}

func TestSaveCache_ContextDeadline(t *testing.T) {
	srv := fakepluginserver.New(t)
	srv.SaveCache(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		assert.NilError(t, json.NewEncoder(w).Encode(map[string]string{"result": "success"}))
	}))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := client.SaveCache(ctx, "k", []string{"."})
	assert.Assert(t, err != nil)
	assert.Assert(t, !errors.Is(err, ErrNoServer))
}
