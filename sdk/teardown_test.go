package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/internal/testing/fakepluginserver"
)

// savePayload is every field the client may send when saving a cache, so a
// test can assert on the ones it does not expect as well as the ones it does.
type savePayload struct {
	Key           string   `json:"key"`
	Paths         []string `json:"paths"`
	Teardown      bool     `json:"teardown"`
	When          string   `json:"when"`
	IgnoreFailure bool     `json:"ignore_failure"`
}

// saveRecorder answers a save with result and records what was sent.
func saveRecorder(t *testing.T, result string, got *savePayload) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NilError(t, json.NewDecoder(r.Body).Decode(got))
		assert.NilError(t, json.NewEncoder(w).Encode(map[string]string{"result": result}))
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
			srv.SaveCache(saveRecorder(t, "deferred", &got))
			client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

			err := tc.save(client.AtTeardown()).SaveCache(context.Background(), "my-key", []string{"./cache"})

			// A deferred result means the save was registered, not that it failed.
			assert.NilError(t, err)
			assert.Equal(t, got.Key, "my-key")
			assert.DeepEqual(t, got.Paths, []string{"./cache"})
			assert.Equal(t, got.Teardown, true)
			assert.Equal(t, got.When, tc.expectedWhen)
			// Best effort, so a failure at the end of the job does not fail the job.
			assert.Equal(t, got.IgnoreFailure, true)
		})
	}
}

// A plain SaveCache must keep sending exactly what it sent before teardown
// existed, so the new fields stay absent rather than defaulted.
func TestSaveCache_SendsNoTeardownFields(t *testing.T) {
	var got savePayload
	srv := fakepluginserver.New(t)
	srv.SaveCache(saveRecorder(t, "success", &got))
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
	srv.SaveCache(saveRecorder(t, "deferred", &got))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	teardown := client.AtTeardown()
	teardown.OnFail()

	err := teardown.SaveCache(context.Background(), "my-key", []string{"./cache"})
	assert.NilError(t, err)
	assert.Equal(t, got.When, whenAlways)
}
