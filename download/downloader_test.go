package download_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/download"
)

// server serves a release at every path, and counts the requests it gets.
func server(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("release"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newDownloader(t *testing.T, dir string) *download.Downloader {
	t.Helper()
	d, err := download.NewDownloader(context.Background(), 10*time.Second, dir)
	assert.NilError(t, err)
	return d
}

func TestDownload(t *testing.T) {
	srv, hits := server(t)
	dir := t.TempDir()

	path, err := newDownloader(t, dir).Download(context.Background(), srv.URL+"/tool/v1/tool.zip", 0600)
	assert.NilError(t, err)
	assert.Equal(t, path, filepath.Join(dir, "tool", "v1", "tool.zip"))

	got, err := os.ReadFile(path) //nolint:gosec // a path this test chose
	assert.NilError(t, err)
	assert.Equal(t, string(got), "release")
	assert.Equal(t, hits.Load(), int32(1))

	entries, err := os.ReadDir(filepath.Dir(path))
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1, "no temporary file may be left beside the download")
}

// A shared directory such as os.TempDir() lets anyone put a file where the
// download would go. Reusing it would install that file instead.
func TestDownload_ReplacesAFileItDidNotDownload(t *testing.T) {
	srv, hits := server(t)
	dir := t.TempDir()
	planted := filepath.Join(dir, "tool.pkg")
	assert.NilError(t, os.WriteFile(planted, []byte("planted"), 0600))

	path, err := newDownloader(t, dir).Download(context.Background(), srv.URL+"/tool.pkg", 0600)
	assert.NilError(t, err)
	assert.Equal(t, path, planted)

	got, err := os.ReadFile(path) //nolint:gosec // a path this test chose
	assert.NilError(t, err)
	assert.Equal(t, string(got), "release")
	assert.Equal(t, hits.Load(), int32(1))
}

// A file one Downloader wrote is no more trustworthy to another, which may be
// a later process.
func TestDownload_DoesNotReuseAnotherDownloadersFile(t *testing.T) {
	srv, hits := server(t)
	dir := t.TempDir()

	_, err := newDownloader(t, dir).Download(context.Background(), srv.URL+"/tool.zip", 0600)
	assert.NilError(t, err)
	_, err = newDownloader(t, dir).Download(context.Background(), srv.URL+"/tool.zip", 0600)
	assert.NilError(t, err)

	assert.Equal(t, hits.Load(), int32(2))
}

func TestDownload_ReusesWhatItDownloaded(t *testing.T) {
	srv, hits := server(t)
	d := newDownloader(t, t.TempDir())

	first, err := d.Download(context.Background(), srv.URL+"/tool.zip", 0600)
	assert.NilError(t, err)
	second, err := d.Download(context.Background(), srv.URL+"/tool.zip", 0600)
	assert.NilError(t, err)

	assert.Equal(t, first, second)
	assert.Equal(t, hits.Load(), int32(1))
}

func TestDownload_FetchesAgainAfterRemove(t *testing.T) {
	srv, hits := server(t)
	d := newDownloader(t, t.TempDir())

	_, err := d.Download(context.Background(), srv.URL+"/tool.zip", 0600)
	assert.NilError(t, err)
	assert.NilError(t, d.Remove(srv.URL+"/tool.zip"))
	_, err = d.Download(context.Background(), srv.URL+"/tool.zip", 0600)
	assert.NilError(t, err)

	assert.Equal(t, hits.Load(), int32(2))
}

func TestDownload_SetsTheMode(t *testing.T) {
	srv, _ := server(t)

	path, err := newDownloader(t, t.TempDir()).Download(context.Background(), srv.URL+"/tool", 0750)
	assert.NilError(t, err)

	info, err := os.Stat(path)
	assert.NilError(t, err)
	assert.Equal(t, info.Mode().Perm(), os.FileMode(0750))
}

func TestDownload_FailureLeavesNothingBehind(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	dir := t.TempDir()

	_, err := newDownloader(t, dir).Download(context.Background(), srv.URL+"/tool.zip", 0600)
	assert.ErrorContains(t, err, "could not get URL")

	entries, err := os.ReadDir(dir)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 0)
}
