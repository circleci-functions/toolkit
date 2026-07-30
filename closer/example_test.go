package closer_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/circleci-functions/toolkit/closer"
)

func ExampleErrorHandler() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "Hello world!")
	}))
	defer srv.Close()

	output, err := run(srv.URL)
	if err != nil {
		fmt.Println(err.Error())
	}
	fmt.Println(output)

	// output: Hello world!
}

func run(rawurl string) (_ string, err error) {
	resp, err := http.Get(rawurl) //nolint:gosec // rawurl is always the httptest.Server URL from the caller above
	if err != nil {
		return "", err
	}
	defer closer.ErrorHandler(resp.Body, &err)

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(b), nil
}
