// Package errs is a ground-truth example for errors.As on errors whose
// dynamic type the model does not know.
package errs

import "errors"

// APIError is an error that carries an HTTP status.
type APIError struct{ Code int }

func (e *APIError) Error() string { return "api error" }

// Status is the HTTP status an error carries, 502 for any other error. It
// holds: errors.As sets the target whenever it reports true.
func Status(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return 502
}
