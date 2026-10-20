// Package meta holds a request's settings, as one-api's relay/meta does: a
// struct of exported fields, and a lookup that returns a *Meta.
package meta

// Meta is plain data a caller fills in field by field.
type Meta struct {
	BaseURL string
	Retries int
}

// Get returns the Meta stored under k. It returns a *Meta, but Meta is not
// a handle only it can build.
func Get(m map[string]*Meta, k string) *Meta { return m[k] }

// Session is a handle: state of its own (an unexported field) that only
// NewSession sets up.
type Session struct {
	Model string
	id    int
}

// NewSession opens a session for a model.
func NewSession(model string) *Session { return &Session{Model: model, id: 1} }
