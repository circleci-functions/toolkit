package sdk

import (
	"context"
	"fmt"
)

type setOutputRequest struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// SetOutput sets the output name for later steps in the job to read. value is
// a string, bool, int or []string, matching the type name is declared with in
// function.yaml; setting name again replaces its value. It returns ErrNoServer
// if the function isn't running inside a CircleCI job.
func (c *Client) SetOutput(ctx context.Context, name string, value any) error {
	switch v := value.(type) {
	case string, bool, int:
	case []string:
		// A nil slice would encode as null rather than a list.
		if v == nil {
			value = []string{}
		}
	default:
		return fmt.Errorf("sdk: output %q is a %T, must be a string, bool, int or []string", name, value)
	}
	_, err := c.do(ctx, "/step/output/set", setOutputRequest{Name: name, Value: value})
	return err
}
