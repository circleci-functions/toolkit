package httpcl

import (
	"context"

	"github.com/circleci-functions/toolkit/application"
)

// Load creates a client configured with the config param.
// See: New
// In addition, it wires the application.Component.
func Load(ctx context.Context, app *application.ManagedApp, cfg Config) *Client {
	srv := New(ctx, cfg)
	app.AddComponent(srv)
	return srv
}
