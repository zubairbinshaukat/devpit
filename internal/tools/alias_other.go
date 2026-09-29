//go:build !windows

package tools

// readAppExecLink is the default [WithReadAlias] outside Windows, which has
// no app execution aliases.
func readAppExecLink(string) (string, error) { return "", errNotAppExecLink }
