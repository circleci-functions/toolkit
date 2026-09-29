package sdktest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
)

func TestServer_RecordsOutputs(t *testing.T) {
	srv := sdktest.NewServer(t)
	ctx := context.Background()
	client := srv.Client()

	assert.NilError(t, client.SetOutput(ctx, "digest", "sha256:abc"))
	assert.NilError(t, client.SetOutput(ctx, "pushed", true))
	assert.NilError(t, client.SetOutput(ctx, "count", 2))
	assert.NilError(t, client.SetOutput(ctx, "tags", []string{"api:1.2.3", "api:latest"}))
	assert.NilError(t, client.SetOutput(ctx, "none", []string(nil)))

	assert.DeepEqual(t, srv.Outputs(), map[string]any{
		"digest": "sha256:abc",
		"pushed": true,
		"count":  2,
		"tags":   []string{"api:1.2.3", "api:latest"},
		"none":   []string{},
	})
}

func TestServer_SetOutputTwice_LastValueWins(t *testing.T) {
	srv := sdktest.NewServer(t)
	ctx := context.Background()

	assert.NilError(t, srv.Client().SetOutput(ctx, "digest", "first"))
	assert.NilError(t, srv.Client().SetOutput(ctx, "digest", "second"))

	assert.DeepEqual(t, srv.Outputs(), map[string]any{"digest": "second"})
}

func TestServer_Outputs_IsACopy(t *testing.T) {
	srv := sdktest.NewServer(t)
	assert.NilError(t, srv.Client().SetOutput(context.Background(), "digest", "sha256:abc"))

	got := srv.Outputs()
	got["digest"] = "changed"

	assert.DeepEqual(t, srv.Outputs(), map[string]any{"digest": "sha256:abc"})
}

// The SDK rejects these before sending, so they are posted directly: the fake
// must not record a value a job would refuse.
func TestServer_SetOutput_RejectsUnsupportedValue(t *testing.T) {
	for _, body := range []string{
		`{"name":"out"}`,
		`{"name":"out","value":null}`,
		`{"name":"out","value":1.5}`,
		`{"name":"out","value":[1,2]}`,
		`{"name":"out","value":{"a":"b"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv := sdktest.NewServer(t)
			resp, err := http.Post(srv.URL()+"/step/output/set", "application/json", strings.NewReader(body))
			assert.NilError(t, err)
			defer resp.Body.Close()

			var got struct {
				Error string `json:"error"`
			}
			assert.NilError(t, json.NewDecoder(resp.Body).Decode(&got))
			assert.Equal(t, resp.StatusCode, http.StatusBadRequest)
			assert.Assert(t, got.Error != "", "the error should reach the SDK as a ServerError")
			assert.Equal(t, len(srv.Outputs()), 0)
		})
	}
}
