package sdk

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/internal/testing/fakepluginserver"
)

func TestSetOutput_Success(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "string", value: "sha256:abc", want: `{"name":"out","value":"sha256:abc"}`},
		{name: "bool", value: true, want: `{"name":"out","value":true}`},
		{name: "int", value: 3, want: `{"name":"out","value":3}`},
		{name: "list", value: []string{"api:1.2.3", "api:latest"}, want: `{"name":"out","value":["api:1.2.3","api:latest"]}`},
		{name: "nil list", value: []string(nil), want: `{"name":"out","value":[]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakepluginserver.New(t)
			var body []byte
			srv.SetOutput(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var err error
				body, err = io.ReadAll(r.Body)
				assert.NilError(t, err)
				fakepluginserver.JSONHandler(t, http.StatusOK, map[string]string{}).ServeHTTP(w, r)
			}))
			client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

			err := client.SetOutput(context.Background(), "out", tt.value)
			assert.NilError(t, err)
			assert.Equal(t, string(body), tt.want)
		})
	}
}

func TestSetOutput_RejectsUnsupportedType(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "nil", value: nil},
		{name: "float", value: 1.5},
		{name: "int64", value: int64(1)},
		{name: "list of ints", value: []int{1}},
		{name: "map", value: map[string]string{"a": "b"}},
		{name: "pointer to string", value: new(string)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakepluginserver.New(t)
			srv.SetOutput(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("no request should be sent for an unsupported type")
			}))
			client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

			err := client.SetOutput(context.Background(), "out", tt.value)
			assert.ErrorContains(t, err, `sdk: output "out" is a`)
			assert.ErrorContains(t, err, "must be a string, bool, int or []string")
		})
	}
}

func TestSetOutput_ServerError(t *testing.T) {
	srv := fakepluginserver.New(t)
	srv.SetOutput(fakepluginserver.JSONHandler(t, http.StatusBadRequest,
		map[string]string{"error": "'digset' is not an output of aws-ecr"}))
	client := newWithSocketPath(fakepluginserver.SocketPath(srv.Dir))

	err := client.SetOutput(context.Background(), "digset", "sha256:abc")

	var svrErr *ServerError
	assert.Assert(t, errors.As(err, &svrErr))
	assert.Equal(t, svrErr.Msg, "'digset' is not an output of aws-ecr")
}

func TestSetOutput_ErrNoServer(t *testing.T) {
	client := newWithSocketPath(fakepluginserver.SocketPath(fakepluginserver.Dir(t)))

	err := client.SetOutput(context.Background(), "out", "value")
	assert.Assert(t, errors.Is(err, ErrNoServer))
}
