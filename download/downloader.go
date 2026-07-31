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
	"time"

	"github.com/circleci-functions/toolkit/closer"
	hc "github.com/circleci-functions/toolkit/httpcl"
	"github.com/circleci-functions/toolkit/observability"
)

// Downloader is a utility for downloading releases of CircleCI services published in the standard CircleCI release
// format from S3.
type Downloader struct {
	dir    string
	client *hc.Client
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
	}, nil
}

// Download downloads the file from the rawURL, to a location rooted at the location specified when constructing
// the downloader nested to a file location based on the path part of the rawURL.
func (d *Downloader) Download(ctx context.Context, rawURL string, perm os.FileMode) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("cannot parse URL: %w", err)
	}

	target := d.targetPath(u)
	tmp := target + ".tmp"

	defer func() {
		cleanup := func() {
			defer os.Remove(target)
			defer os.Remove(tmp)
		}

		// Don't leave half-downloaded or invalid downloads hanging around
		if p := recover(); p != nil {
			cleanup()
			panic(p)
		} else if err != nil {
			cleanup()
		}
	}()

	if isCached(ctx, target) {
		return target, nil
	}

	err = d.downloadFile(ctx, u.String(), tmp, perm)
	if err != nil {
		return "", err
	}

	err = os.Rename(tmp, target)
	if err != nil {
		return "", err
	}

	return target, nil
}

// Remove removes any file that the downloader had previously downloaded from the rawURL
func (d *Downloader) Remove(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("cannot parse URL: %w", err)
	}
	err = os.Remove(d.targetPath(u))
	e := &os.PathError{}
	if errors.As(err, &e) {
		return nil
	}
	return err
}

func (d *Downloader) targetPath(u *url.URL) string {
	return filepath.Join(d.dir, u.Path)
}

func isCached(ctx context.Context, target string) bool {
	info, err := os.Stat(target)
	if err != nil {
		if !os.IsNotExist(err) {
			observability.SetSpanAttribute(ctx, "downloader_error", err)
		}
		return false
	}
	return !info.IsDir()
}

func (d *Downloader) downloadFile(ctx context.Context, url, target string, perm os.FileMode) (err error) {
	err = os.MkdirAll(filepath.Dir(target), 0755) // #nosec - the downloads are intentionally world-readable
	if err != nil {
		return fmt.Errorf("could not create directory: %w", err)
	}

	//#nosec:G304 // this is an executable binary, these permissions are needed.
	out, err := os.OpenFile(target, os.O_RDWR|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("could not create file: %w", err)
	}
	defer closer.ErrorHandler(out, &err)

	var resp []byte
	err = d.client.Call(ctx, hc.NewRequest("GET", url,
		hc.Timeout(30*time.Second),
		hc.BytesDecoder(&resp)),
	)

	if err != nil {
		return fmt.Errorf("could not get URL %q: %w", url, err)
	}

	_, err = io.Copy(out, bytes.NewBuffer(resp))
	if err != nil {
		return fmt.Errorf("could not write file %q: %w", target, err)
	}

	return nil
}
