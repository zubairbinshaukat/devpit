package netstat

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"syscall"

	"github.com/zubairbinshaukat/devpit/internal/share/errmap"
)

// Share is one shared folder of another PC.
type Share struct {
	// Name is the share name, as typed after the PC name in a UNC path.
	Name string
	// Type is the column after the name, in the PC's language ("Disk",
	// "Platte", "Disque"). It is shown as it came and never compared.
	Type string
	// Comment is the last column, often empty.
	Comment string
}

// Runner runs a console program and returns what it printed. The real one
// decodes the OEM code page; tests return recorded text.
type Runner interface {
	// Run runs name with args. exit is the process exit code; err is
	// non-nil only when the process could not be started or was cancelled.
	Run(ctx context.Context, name string, args ...string) (out string, exit int, err error)
}

// rule matches the dashed line `net view` draws under its column headers.
var rule = regexp.MustCompile(`^-{10,}\s*$`)

// columnGap splits a row into columns: two or more spaces, or a tab.
var columnGap = regexp.MustCompile(`\s{2,}|\t`)

// ParseNetView reads the output of `net view \\host`.
//
//	Shared resources at \\192.168.1.5
//
//	Share name  Type  Used as  Comment
//
//	-------------------------------------------------------------------
//	Games       Disk           My games
//	Movies      Disk
//	The command completed successfully.
//
// Every word above except the share names is translated, so the parser
// looks for structure: the table starts after the dashed line and ends at
// the last line, which is the "completed" sentence. That sentence is
// told from a row because it has no two-space gap in it and a row always
// has one between the name and the type. Names ending in $ are the hidden
// administrative shares (C$, ADMIN$, IPC$) and are dropped: their names are
// the same on every language.
func ParseNetView(out string) []Share {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if rule.MatchString(strings.TrimRight(l, " \t")) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	var rows []string
	for _, l := range lines[start:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		rows = append(rows, l)
	}
	if n := len(rows); n > 0 && !columnGap.MatchString(strings.TrimSpace(rows[n-1])) {
		rows = rows[:n-1]
	}
	var shares []Share
	for _, r := range rows {
		f := columnGap.Split(strings.TrimRight(r, " \t"), 3)
		name := strings.TrimSpace(f[0])
		if name == "" || strings.HasSuffix(name, "$") {
			continue
		}
		s := Share{Name: name}
		if len(f) > 1 {
			s.Type = strings.TrimSpace(f[1])
		}
		if len(f) > 2 {
			s.Comment = strings.TrimSpace(f[2])
		}
		shares = append(shares, s)
	}
	return shares
}

// ListShares runs `net view \\host` and returns its shares. A failure comes
// back as a [syscall.Errno] holding the number in net's message, so
// errmap can explain it in any Windows language.
func ListShares(ctx context.Context, r Runner, host string) ([]Share, error) {
	out, exit, err := r.Run(ctx, "net", "view", UNC(host, ""))
	if err != nil {
		return nil, fmt.Errorf("net view: %w", err)
	}
	if exit != 0 {
		if code, ok := errmap.CodeFromText(out); ok {
			return nil, fmt.Errorf("listing shares of %s: %w", host, syscall.Errno(code))
		}
		return nil, fmt.Errorf("listing shares of %s: net view exited with %d", host, exit)
	}
	return ParseNetView(out), nil
}

// uncToken finds \\host\share words in the output of `net use`.
var uncToken = regexp.MustCompile(`\\\\[^\s\\]+\\[^\s]+`)

// ParseConnections returns the remote names in the output of a bare `net
// use`, whose table lists the open connections. Each is a \\host\share word
// found by shape, so the translated column headers do not matter.
func ParseConnections(out string) []string {
	return uncToken.FindAllString(out, -1)
}

// ConnectionsTo returns the open connections to host.
func ConnectionsTo(ctx context.Context, r Runner, host string) ([]string, error) {
	out, exit, err := r.Run(ctx, "net", "use")
	if err != nil {
		return nil, fmt.Errorf("net use: %w", err)
	}
	if exit != 0 {
		return nil, fmt.Errorf("net use exited with %d", exit)
	}
	prefix := strings.ToLower(`\\` + host + `\`)
	var mine []string
	for _, c := range ParseConnections(out) {
		if strings.HasPrefix(strings.ToLower(c), prefix) {
			mine = append(mine, c)
		}
	}
	return mine, nil
}
