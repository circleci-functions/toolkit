package sdk

import "context"

type storeArtifactsRequest struct {
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`

	// Set when the store is registered to run at the end of the job.
	Teardown bool   `json:"teardown,omitempty"`
	When     string `json:"when,omitempty"`
}

type storeTestResultsRequest struct {
	Path string `json:"path"`

	// Set when the store is registered to run at the end of the job.
	Teardown bool   `json:"teardown,omitempty"`
	When     string `json:"when,omitempty"`
}

// StoreArtifacts stores the file or directory at path as a job artifact,
// available for download from the CircleCI web app once the job completes.
// destination sets the path shown in the artifacts list; if empty, path
// itself is used, relative to the filesystem root.
//
// It returns ErrNoServer if the function isn't running inside a CircleCI job.
func (c *Client) StoreArtifacts(ctx context.Context, path, destination string) error {
	_, err := c.do(ctx, "/step/artifacts/store", storeArtifactsRequest{
		Path:        path,
		Destination: destination,
	})
	return err
}

// StoreTestResults stores the XML test-result files under path so CircleCI
// can parse them and surface timing and failure information in the web app.
//
// It returns ErrNoServer if the function isn't running inside a CircleCI job.
func (c *Client) StoreTestResults(ctx context.Context, path string) error {
	_, err := c.do(ctx, "/step/test-results/store", storeTestResultsRequest{Path: path})
	return err
}
