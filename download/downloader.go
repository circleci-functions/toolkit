/*
Package download helps download releases of binaries.
*/
package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	hc "github.com/circleci-functions/toolkit/httpcl"
	"github.com/circleci-functions/toolkit/observability"
)

// Downloader is a utility for downloading releases of CircleCI services published in the standard CircleCI release
// format from S3.
type Downloader struct {
	dir    string
	client *hc.Client

	mu sync.Mutex
	// downloaded holds the targets this Downloader wrote, the only files it will
	// hand back without fetching them again.
	downloaded map[string]bool
}

// NewDownloader is the constructor for Downloader
func NewDownloader(ctx context.Context, timeout time.Duration, dir string) (*Downloader, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("could not absolutify downloader dir: %w", err)
	}

	err = os.MkdirAll(dir, 0755) // #nosec - the downloads are intentionally world-readable
	if err != nil {
		return nil, fmt.Errorf("could not create e2e-test dir: %w", err)
	}

	return &Downloader{
		dir: dir,
		client: hc.New(ctx, hc.Config{
			Timeout: timeout,
		}),
		downloaded: map[string]bool{},
	}, nil
}

// Download downloads the file from the rawURL, to a location rooted at the location specified when constructing
// the downloader nested to a file location based on the path part of the rawURL.
//
// A file already at that location is reused only if this Downloader downloaded it. Anything else is fetched again
// and replaced: the directory may be shared, such as os.TempDir(), and a file someone else left there could be
// installed in place of the real download.
func (d *Downloader) Download(ctx context.Context, rawURL string, perm os.FileMode) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("cannot parse URL: %w", err)
	}

	target := d.targetPath(u)

	d.mu.Lock()
	reuse := d.downloaded[target]
	d.mu.Unlock()
	if reuse && isFile(ctx, target) {
		return target, nil
	}

	if err := d.downloadFile(ctx, u.String(), target, perm); err != nil {
		return "", err
	}

	d.mu.Lock()
	d.downloaded[target] = true
	d.mu.Unlock()
	return target, nil
}

// Remove removes any file that the downloader had previously downloaded from the rawURL
func (d *Downloader) Remove(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("cannot parse URL: %w", err)
	}
	target := d.targetPath(u)

	d.mu.Lock()
	delete(d.downloaded, target)
	d.mu.Unlock()

	err = os.Remove(target)
	e := &os.PathError{}
	if errors.As(err, &e) {
		return nil
	}
	return err
}

func (d *Downloader) targetPath(u *url.URL) string {
	return filepath.Join(d.dir, u.Path)
}

func isFile(ctx context.Context, target string) bool {
	info, err := os.Stat(target)
	if err != nil {
		if !os.IsNotExist(err) {
			observability.SetSpanAttribute(ctx, "downloader_error", err)
		}
		return false
	}
	return !info.IsDir()
}

// downloadFile writes to a new temporary file beside target and renames it into place, so nothing at target, or
// at a predictable temporary name, is opened and written through.
func (d *Downloader) downloadFile(ctx context.Context, url, target string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { // #nosec - the downloads are intentionally world-readable
		return fmt.Errorf("could not create directory: %w", err)
	}

	out, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".*.tmp")
	if err != nil {
		return fmt.Errorf("could not create file: %w", err)
	}
	tmp := out.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = out.Close()
			_ = os.Remove(tmp)
		}
	}()

	var resp []byte
	err = d.client.Call(ctx, hc.NewRequest("GET", url,
		hc.Timeout(30*time.Second),
		hc.BytesDecoder(&resp)),
	)
	if err != nil {
		return fmt.Errorf("could not get URL %q: %w", url, err)
	}

	if _, err = io.Copy(out, bytes.NewBuffer(resp)); err != nil {
		return fmt.Errorf("could not write file %q: %w", target, err)
	}
	if err = out.Chmod(perm); err != nil {
		return fmt.Errorf("could not set mode of %q: %w", target, err)
	}
	if err = out.Close(); err != nil {
		return fmt.Errorf("could not write file %q: %w", target, err)
	}
	if err = os.Rename(tmp, target); err != nil {
		return fmt.Errorf("could not replace %q: %w", target, err)
	}
	renamed = true
	return nil
}
