package launch

import (
	"fmt"
	"strings"
)

// Go's os/exec quotes arguments for programs that parse their command line
// the Microsoft C runtime way. cmd.exe does not: it reads the line itself
// first. The Phase 0 spike (plans/v0.4.0-spikes.md §3) showed Go 1.27.1
// passing `"&echo INJECTED>x&"` to an npm .cmd ran the echo, `%PATH%`
// expanded, `^` vanished and `<x>` became a redirection, all with no error.
// os/exec documents that a batch file needs its own quoting; it does none.
//
// So a .cmd or .bat is never started with arbitrary arguments:
//
//   - an npm cmd-shim (the common case: vercel, firebase, supabase,
//     wrangler, claude from npm) is parsed and its node script is started
//     with node.exe directly, which passes every argument exactly;
//   - any other script runs through cmd.exe only when every argument is
//     made of characters that cmd.exe cannot misread ([SafeBatchArg]), with
//     the command line built by hand; anything else is refused before
//     anything starts.

// SafeBatchArg reports whether a can be handed to an arbitrary .cmd or .bat
// unchanged: letters, digits, space and _ - . , : / \ = @ +, nothing else.
// The empty string is safe (it is passed as "").
func SafeBatchArg(a string) bool {
	for i := 0; i < len(a); i++ {
		c := a[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte(" _-.,:/\\=@+", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// BatchCommandLine returns the full command line that runs script through
// cmdExe with args. It refuses (with an error wrapping ErrUnsafeArgument)
// a script path cmd.exe would change and any argument that is not
// [SafeBatchArg]. Arguments with a space, ',' or '=' are quoted, since cmd
// splits parameters on those; a trailing backslash is doubled inside the
// quotes so it cannot escape the closing quote. The whole command sits in
// one extra pair of quotes that /s strips.
func BatchCommandLine(cmdExe, script string, args []string) (string, error) {
	if script == "" || strings.ContainsAny(script, "\"%^&|<>!\r\n\x00") || strings.HasSuffix(script, `\`) {
		return "", fmt.Errorf("%w: the script path %q has a character cmd.exe would change", ErrUnsafeArgument, script)
	}
	var b strings.Builder
	b.WriteString(`"` + cmdExe + `" /d /e:ON /v:OFF /s /c ""` + script + `"`)
	for i, a := range args {
		if !SafeBatchArg(a) {
			return "", fmt.Errorf("%w: argument %d (%q) has a character that %s would treat as a command (only letters, digits, spaces and _ - . , : / \\ = @ + are passed to a .cmd or .bat)",
				ErrUnsafeArgument, i+1, a, script)
		}
		b.WriteByte(' ')
		if a == "" || strings.ContainsAny(a, " ,=") || strings.HasSuffix(a, `\`) {
			trail := len(a) - len(strings.TrimRight(a, `\`))
			b.WriteString(`"` + a + strings.Repeat(`\`, trail) + `"`)
			continue
		}
		b.WriteString(a)
	}
	b.WriteByte('"')
	return b.String(), nil
}
