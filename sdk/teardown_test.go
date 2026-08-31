package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/internal/testing/fakepluginserver"
)

// savePayload is every field the client may send when saving a cache, plus
// ignore_failure which it must never send, so a test can assert on the fields it
// does not expect as well as the ones it does.
type savePayload struct {
	Key           string   `json:"key"`
	Paths         []string `json:"paths"`
	Name          string   `json:"name"`
	Teardown      bool     `json:"teardown"`
	When          string   `json:"when"`
	IgnoreFailure bool     `json:"ignore_failure"`
}

// saveRecorder answers a save with status and body, and records what was sent.
// Both matter: the agent accepts a deferred step with 202 and {"deferred":true},
// and reports a step it actually ran with 200 and a result.
func saveRecorder(t *testing.T, status int, body map[string]any, got *savePayload) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NilError(t, json.NewDecoder(r.Body).Decode(got))
		w.WriteHeader(status)
		assert.NilError(t, json.NewEncoder(w).Encode(body))
	})
}

func TestAtTeardown_SaveCache(t *testing.T) {
	testcases := []struct {
		doc          string
		save         func(*Teardown) *Teardown
		expectedWhen string
	}{
		{
			doc:          "runs whatever the job outcome by default",
			save:         func(td *Teardown) *Teardown { return td },
			expectedWhen: whenAlways,
		},
		{
			doc:          "OnFail runs only for a failed job",
			save:         func(td *Teardown) *Teardown { return td.OnFail() },
			expectedWhen: whenOnFail,
		},
		{
			doc:          "OnSuccess runs only for a successful job",
			save:         func(td *Teardown) *Teardown { return td.OnSuccess() },
			expectedWhen: whenOnSuccess,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.doc, func(t *testing.T) {
			var got savePayload
			srv := fakepluginserver.New(t)
			srv.SaveCache(saveRecorder(t, http.StatusAccepted, map[string]any{"deferred": true}, &got))
			client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

			err := tc.save(client.AtTeardown()).SaveCache(context.Background(), "my-key", []string{"./cache"})

			// A 202 means the save was registered, not that it failed.
			assert.NilError(t, err)
			assert.Equal(t, got.Key, "my-key")
			assert.DeepEqual(t, got.Paths, []string{"./cache"})
			assert.Equal(t, got.Teardown, true)
			assert.Equal(t, got.When, tc.expectedWhen)
			// The agent rejects ignore_failure outright: a cache save reports
			// success whatever happens to it, so there is nothing to opt out of.
			assert.Equal(t, got.IgnoreFailure, false)
		})
	}
}

// A plain SaveCache must keep sending exactly what it sent before teardown
// existed, so the new fields stay absent rather than defaulted.
func TestSaveCache_SendsNoTeardownFields(t *testing.T) {
	var got savePayload
	srv := fakepluginserver.New(t)
	srv.SaveCache(saveRecorder(t, http.StatusOK, map[string]any{"result": "success"}, &got))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	err := client.SaveCache(context.Background(), "my-key", []string{"./cache"})
	assert.NilError(t, err)

	assert.Equal(t, got.Teardown, false)
	assert.Equal(t, got.When, "")
	assert.Equal(t, got.IgnoreFailure, false)
}

// A condition is chosen per registration, so asking for one does not change
// the client other calls go through.
func TestAtTeardown_ConditionDoesNotLeak(t *testing.T) {
	var got savePayload
	srv := fakepluginserver.New(t)
	srv.SaveCache(saveRecorder(t, http.StatusAccepted, map[string]any{"deferred": true}, &got))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	teardown := client.AtTeardown()
	teardown.OnFail()

	err := teardown.SaveCache(context.Background(), "my-key", []string{"./cache"})
	assert.NilError(t, err)
	assert.Equal(t, got.When, whenAlways)
}

// TestSaveCache_WithStepName verifies the step name reaches the wire, so a job
// can label a cache step instead of showing the generic default.
func TestSaveCache_WithStepName(t *testing.T) {
	var got savePayload
	srv := fakepluginserver.New(t)
	srv.SaveCache(saveRecorder(t, http.StatusAccepted, map[string]any{"deferred": true}, &got))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	err := client.AtTeardown().SaveCache(context.Background(), "my-key", []string{"./cache"},
		WithStepName("Saving go build cache"))
	assert.NilError(t, err)
	assert.Equal(t, got.Name, "Saving go build cache")
}
