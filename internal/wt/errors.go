package wt

import "fmt"

// UnparsableError indicates settings.json could not be parsed as HuJSON
// (or its top-level value was not a JSON object). Patch leaves the file
// untouched when this is returned; the caller should show ManualSnippet
// instead.
type UnparsableError struct {
	Path string
	Err  error
}

func (e *UnparsableError) Error() string {
	return fmt.Sprintf("wt: %s is not valid HuJSON: %v", e.Path, e.Err)
}

func (e *UnparsableError) Unwrap() error { return e.Err }
