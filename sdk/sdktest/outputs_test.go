package sdktest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk"
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
	assert.NilError(t, srv.Client().SetOutput(ctx, "digest", 2))

	assert.DeepEqual(t, srv.Outputs(), map[string]any{"digest": 2})
}

func TestServer_Outputs_IsACopy(t *testing.T) {
	srv := sdktest.NewServer(t)
	assert.NilError(t, srv.Client().SetOutput(context.Background(), "tags", []string{"a"}))

	got := srv.Outputs()
	got["tags"].([]string)[0] = "changed"
	got["other"] = "added"

	assert.DeepEqual(t, srv.Outputs(), map[string]any{"tags": []string{"a"}})
}

func TestServer_SetOutput_RefusesWhatAJobRefuses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "no value", body: `{"name":"out"}`, want: "output 'out' must be a string, bool, int or list"},
		{name: "null", body: `{"name":"out","value":null}`, want: "output 'out' must be a string, bool, int or list"},
		{name: "float", body: `{"name":"out","value":1.5}`, want: "output 'out' must be a string, bool, int or list"},
		{name: "float spelled as a whole number", body: `{"name":"out","value":1.0}`, want: "output 'out' must be a string, bool, int or list"},
		{name: "object", body: `{"name":"out","value":{"a":"b"}}`, want: "output 'out' must be a string, bool, int or list"},
		{name: "list of ints", body: `{"name":"out","value":[1,2]}`, want: "output 'out' must be a string, bool, int or list"},
		{name: "mixed list", body: `{"name":"out","value":["a",1]}`, want: "output 'out' must be a string, bool, int or list"},
		{name: "unknown field", body: `{"name":"out","value":"x","teardown":true}`, want: `json: unknown field "teardown"`},
		{name: "NUL in a string", body: `{"name":"out","value":"a\u0000b"}`, want: "output 'out' contains a NUL byte"},
		{name: "NUL in a list item", body: `{"name":"out","value":["a","b\u0000"]}`, want: "output 'out' contains a NUL byte"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := sdktest.NewServer(t)
			resp, err := http.Post(srv.URL()+"/step/output/set", "application/json", strings.NewReader(tt.body))
			assert.NilError(t, err)
			defer resp.Body.Close()

			var got struct {
				Error string `json:"error"`
			}
			assert.NilError(t, json.NewDecoder(resp.Body).Decode(&got))
			assert.Equal(t, resp.StatusCode, http.StatusBadRequest)
			assert.Equal(t, got.Error, tt.want)
			assert.Equal(t, len(srv.Outputs()), 0)
		})
	}
}

func TestServer_SetOutput_RefusalReachesTheFunctionAsAServerError(t *testing.T) {
	srv := sdktest.NewServer(t)

	err := srv.Client().SetOutput(context.Background(), "out", "a\x00b")

	var svrErr *sdk.ServerError
	assert.Assert(t, errors.As(err, &svrErr))
	assert.Equal(t, svrErr.Msg, "output 'out' contains a NUL byte")
}
