package accounts

import (
	"fmt"
	"strings"
)

// Tool names one developer tool whose account Devpit can show or switch.
// The value is what accounts.toml and the command line use.
type Tool string

// The eight tools, in the order the Accounts page lists them.
const (
	ToolClaude     Tool = "claude"
	ToolGit        Tool = "git"
	ToolGitHub     Tool = "github"
	ToolVercel     Tool = "vercel"
	ToolFirebase   Tool = "firebase"
	ToolSupabase   Tool = "supabase"
	ToolCloudflare Tool = "cloudflare"
	ToolConvex     Tool = "convex"
)

// Tools returns every tool in display order. It is a fresh slice each call.
func Tools() []Tool {
	return []Tool{
		ToolClaude, ToolGit, ToolGitHub, ToolVercel,
		ToolFirebase, ToolSupabase, ToolCloudflare, ToolConvex,
	}
}

// Known reports whether t is one of the eight tools.
func (t Tool) Known() bool {
	return t.order() >= 0
}

// order is t's position in [Tools], or -1.
func (t Tool) order() int {
	for i, k := range Tools() {
		if k == t {
			return i
		}
	}
	return -1
}

// DisplayName is the name a person knows the tool by.
func (t Tool) DisplayName() string {
	switch t {
	case ToolClaude:
		return "Claude Code"
	case ToolGit:
		return "Git"
	case ToolGitHub:
		return "GitHub"
	case ToolVercel:
		return "Vercel"
	case ToolFirebase:
		return "Firebase"
	case ToolSupabase:
		return "Supabase"
	case ToolCloudflare:
		return "Cloudflare"
	case ToolConvex:
		return "Convex"
	}
	return string(t)
}

// Binary is the program name the tool is started by on the command line,
// without ".exe": "gh" for GitHub, "wrangler" for Cloudflare.
func (t Tool) Binary() string {
	switch t {
	case ToolGitHub:
		return "gh"
	case ToolCloudflare:
		return "wrangler"
	}
	return string(t)
}

// ShimName is the file name (without ".exe") a shim for t takes, or "" when
// the tool is never shimmed: Git applies its rules through its own config
// files, Wrangler through its own folder bindings (which also cover `npx
// wrangler`, and a shim adding --profile would break `wrangler auth …`),
// and Convex is only reachable through npx. "Just this once" works for
// every tool that has it through `devpit <tool> run`, shim or not.
func (t Tool) ShimName() string {
	switch t {
	case ToolGit, ToolConvex, ToolCloudflare:
		return ""
	}
	if !t.Known() {
		return ""
	}
	return t.Binary()
}

// ParseTool turns what a person typed into a Tool. It accepts the tool
// names, their binaries ("gh", "wrangler") and is case-insensitive.
func ParseTool(s string) (Tool, error) {
	k := strings.ToLower(strings.TrimSpace(s))
	k = strings.TrimSuffix(k, ".exe")
	for _, t := range Tools() {
		if k == string(t) || k == t.Binary() {
			return t, nil
		}
	}
	names := make([]string, 0, 8)
	for _, t := range Tools() {
		names = append(names, string(t))
	}
	return "", fmt.Errorf("%q is not a tool Devpit knows. Use one of: %s", s, strings.Join(names, ", "))
}

// ToolForShim maps a shim's own file name (with or without ".exe") back to
// its tool. ok is false for anything Devpit never creates a shim for.
func ToolForShim(name string) (Tool, bool) {
	k := strings.ToLower(strings.TrimSuffix(strings.ToLower(name), ".exe"))
	for _, t := range Tools() {
		if s := t.ShimName(); s != "" && s == k {
			return t, true
		}
	}
	return "", false
}
