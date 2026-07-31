// Package rollbar is a low-level helper for Rollbar
package rollbar

import (
	"github.com/rollbar/rollbar-go"

	"github.com/circleci-functions/toolkit/internal/report"
	"github.com/circleci-functions/toolkit/secret"
)

// Client wraps a Rollbar client
type Client struct {
	cfg    ClientConfig
	client *rollbar.Client
}

var _ report.ErrorEmitter = (*Client)(nil)

// ClientConfig configures a Client
type ClientConfig struct {
	Token       secret.String
	Endpoint    string
	Environment string
	CodeVersion string
	ServerHost  string
	Level       report.Level
}

// NewClient creates a new Client
func NewClient(cfg ClientConfig) *Client {
	client := rollbar.New(
		cfg.Token.Value(),
		cfg.Environment,
		cfg.CodeVersion,
		cfg.ServerHost,
		"/")
	client.SetEnabled(true)
	if cfg.Endpoint != "" {
		client.SetEndpoint(cfg.Endpoint)
	}
	return &Client{
		cfg:    cfg,
		client: client,
	}
}

// SendDeployment sends a Rollbar message that there was a deployment
func (e *Client) SendDeployment() {
	e.client.Message(rollbar.INFO, "Deployment")
}

// ReportError sends the error to Rollbar
func (e *Client) ReportError(level report.Level, err error, extras map[string]any) {
	if e.cfg.Level <= level {
		var rollbarLevel string
		switch level {
		case report.DebugLevel:
			rollbarLevel = rollbar.DEBUG
		case report.InfoLevel:
			rollbarLevel = rollbar.INFO
		case report.WarnLevel:
			rollbarLevel = rollbar.WARN
		case report.ErrorLevel:
			rollbarLevel = rollbar.ERR
		case report.PanicLevel, report.FatalLevel:
			rollbarLevel = rollbar.CRIT
		}
		e.client.ErrorWithExtras(rollbarLevel, err, extras)
	}
}

// Close flushes and closes the Client
func (e *Client) Close() error {
	return e.client.Close()
}
