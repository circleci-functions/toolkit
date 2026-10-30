package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/internal/testing/fakepluginserver"
)

// artifactsPayload captures every field the client may send for a
// StoreArtifacts request.
type artifactsPayload struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
	Teardown    bool   `json:"teardown"`
	When        string `json:"when"`
}

// testResultsPayload captures every field the client may send for a
// StoreTestResults request.
type testResultsPayload struct {
	Path     string `json:"path"`
	Teardown bool   `json:"teardown"`
	When     string `json:"when"`
}

// artifactsRecorder answers a store request with status and body, recording
// what was sent.
func artifactsRecorder(t *testing.T, status int, body map[string]any, got *artifactsPayload) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NilError(t, json.NewDecoder(r.Body).Decode(got))
		w.WriteHeader(status)
		assert.NilError(t, json.NewEncoder(w).Encode(body))
	})
}

// testResultsRecorder answers a store request with status and body, recording
// what was sent.
func testResultsRecorder(t *testing.T, status int, body map[string]any, got *testResultsPayload) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NilError(t, json.NewDecoder(r.Body).Decode(got))
		w.WriteHeader(status)
		assert.NilError(t, json.NewEncoder(w).Encode(body))
	})
}

func TestStoreArtifacts_Success(t *testing.T) {
	var got artifactsPayload
	srv := fakepluginserver.New(t)
	srv.StoreArtifacts(artifactsRecorder(t, http.StatusOK, map[string]any{"result": "success"}, &got))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	err := client.StoreArtifacts(context.Background(), "./bin/tool", "bin/tool")
	assert.NilError(t, err)
	assert.Equal(t, got.Path, "./bin/tool")
	assert.Equal(t, got.Destination, "bin/tool")
	assert.Equal(t, got.Teardown, false)
	assert.Equal(t, got.When, "")
}

func TestStoreArtifacts_Deferred(t *testing.T) {
	var got artifactsPayload
	srv := fakepluginserver.New(t)
	srv.StoreArtifacts(artifactsRecorder(t, http.StatusAccepted, map[string]any{"deferred": true}, &got))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	// A 202 accepted response is success for a deferred store.
	err := client.AtTeardown().StoreArtifacts(context.Background(), "./bin/tool", "bin/tool")
	assert.NilError(t, err)
	assert.Equal(t, got.Teardown, true)
	assert.Equal(t, got.When, whenAlways)
}

func TestStoreArtifacts_TeardownConditions(t *testing.T) {
	testcases := []struct {
		doc          string
		teardown     func(*Teardown) *Teardown
		expectedWhen string
	}{
		{
			doc:          "runs whatever the job outcome by default",
			teardown:     func(td *Teardown) *Teardown { return td },
			expectedWhen: whenAlways,
		},
		{
			doc:          "OnFail runs only for a failed job",
			teardown:     func(td *Teardown) *Teardown { return td.OnFail() },
			expectedWhen: whenOnFail,
		},
		{
			doc:          "OnSuccess runs only for a successful job",
			teardown:     func(td *Teardown) *Teardown { return td.OnSuccess() },
			expectedWhen: whenOnSuccess,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.doc, func(t *testing.T) {
			var got artifactsPayload
			srv := fakepluginserver.New(t)
			srv.StoreArtifacts(artifactsRecorder(t, http.StatusAccepted, map[string]any{"deferred": true}, &got))
			client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

			err := tc.teardown(client.AtTeardown()).StoreArtifacts(context.Background(), "./dist", "dist")
			assert.NilError(t, err)
			assert.Equal(t, got.Teardown, true)
			assert.Equal(t, got.When, tc.expectedWhen)
		})
	}
}

func TestStoreArtifacts_ServerError(t *testing.T) {
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
			srv.StoreArtifacts(fakepluginserver.JSONHandler(t, tt.status, map[string]string{"error": "boom"}))
			client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

			err := client.StoreArtifacts(context.Background(), "./dist", "")
			assert.Assert(t, err != nil)

			var svrErr *ServerError
			assert.Assert(t, errors.As(err, &svrErr))
			assert.Equal(t, svrErr.Msg, "boom")
		})
	}
}

func TestStoreArtifacts_NoServer(t *testing.T) {
	client := newWithSocketPath(fakepluginserver.SocketPath(fakepluginserver.Dir(t)))
	err := client.StoreArtifacts(context.Background(), "./dist", "")
	assert.Assert(t, errors.Is(err, ErrNoServer))
}

func TestStoreTestResults_Success(t *testing.T) {
	var got testResultsPayload
	srv := fakepluginserver.New(t)
	srv.StoreTestResults(testResultsRecorder(t, http.StatusOK, map[string]any{"result": "success"}, &got))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	err := client.StoreTestResults(context.Background(), "./test-results")
	assert.NilError(t, err)
	assert.Equal(t, got.Path, "./test-results")
	assert.Equal(t, got.Teardown, false)
	assert.Equal(t, got.When, "")
}

func TestStoreTestResults_Deferred(t *testing.T) {
	var got testResultsPayload
	srv := fakepluginserver.New(t)
	srv.StoreTestResults(testResultsRecorder(t, http.StatusAccepted, map[string]any{"deferred": true}, &got))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	err := client.AtTeardown().StoreTestResults(context.Background(), "./test-results")
	assert.NilError(t, err)
	assert.Equal(t, got.Teardown, true)
	assert.Equal(t, got.When, whenAlways)
}

func TestStoreTestResults_TeardownConditions(t *testing.T) {
	testcases := []struct {
		doc          string
		teardown     func(*Teardown) *Teardown
		expectedWhen string
	}{
		{
			doc:          "runs whatever the job outcome by default",
			teardown:     func(td *Teardown) *Teardown { return td },
			expectedWhen: whenAlways,
		},
		{
			doc:          "OnFail runs only for a failed job",
			teardown:     func(td *Teardown) *Teardown { return td.OnFail() },
			expectedWhen: whenOnFail,
		},
		{
			doc:          "OnSuccess runs only for a successful job",
			teardown:     func(td *Teardown) *Teardown { return td.OnSuccess() },
			expectedWhen: whenOnSuccess,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.doc, func(t *testing.T) {
			var got testResultsPayload
			srv := fakepluginserver.New(t)
			srv.StoreTestResults(testResultsRecorder(t, http.StatusAccepted, map[string]any{"deferred": true}, &got))
			client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

			err := tc.teardown(client.AtTeardown()).StoreTestResults(context.Background(), "./test-results")
			assert.NilError(t, err)
			assert.Equal(t, got.Teardown, true)
			assert.Equal(t, got.When, tc.expectedWhen)
		})
	}
}

func TestStoreTestResults_ServerError(t *testing.T) {
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
			srv.StoreTestResults(fakepluginserver.JSONHandler(t, tt.status, map[string]string{"error": "boom"}))
			client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

			err := client.StoreTestResults(context.Background(), "./test-results")
			assert.Assert(t, err != nil)

			var svrErr *ServerError
			assert.Assert(t, errors.As(err, &svrErr))
			assert.Equal(t, svrErr.Msg, "boom")
		})
	}
}

func TestStoreTestResults_NoServer(t *testing.T) {
	client := newWithSocketPath(fakepluginserver.SocketPath(fakepluginserver.Dir(t)))
	err := client.StoreTestResults(context.Background(), "./test-results")
	assert.Assert(t, errors.Is(err, ErrNoServer))
}
