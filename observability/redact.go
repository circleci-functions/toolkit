package observability

import (
	"errors"
	"regexp"
)

var reCircleToken = regexp.MustCompile(`circle-token=\w+`)

// redactCircleToken redacts any `circle-token` values from an error. If
// redacted, then a flattened copy will be returned, otherwise the original
// will be returned.
// N.B. This should be used when the error is about to be emitted in o11y, since the returned error
// loses all error type information.
func redactCircleToken(err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if !reCircleToken.MatchString(err.Error()) {
		return false, err
	}
	return true, errors.New(reCircleToken.ReplaceAllString(err.Error(), "circle-token=REDACTED"))
}
