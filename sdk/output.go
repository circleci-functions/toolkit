package sdk

import (
	"context"
	"fmt"
)

type setOutputRequest struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// SetOutput sets the output called name, for later steps in the job to read.
// The function declares its outputs in function.yaml, and value must be the
// Go type of the declared type: a string, bool, int or []string (a list).
// Setting a name again replaces its value.
//
// It returns ErrNoServer if the function isn't running inside a CircleCI job,
// and ErrUnsupported if the job's build agent is too old to take outputs.
func (c *Client) SetOutput(ctx context.Context, name string, value any) error {
	switch v := value.(type) {
	case string, bool, int:
	case []string:
		if v == nil {
			value = []string{}
		}
	default:
		return fmt.Errorf("sdk: output %q is a %T, must be a string, bool, int or []string", name, value)
	}
	_, err := c.do(ctx, "/step/output/set", setOutputRequest{Name: name, Value: value})
	return err
}
