package scan_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The fixture tree these helpers build is the same shape as the one
// testdata/trees/gen.ps1 produces, and deliberately so. gen.ps1 exists for
// benchmarking and for poking at by hand; the tests build their own copy in
// t.TempDir() so that CI never needs PowerShell and never leaves anything
// behind.
//
// Every case in the tree is there because it once broke a tool somebody
// shipped: a project vendored inside node_modules, a node_modules with no
// package.json, a junction pointing back at real source, a "build" folder
// that is somebody's code rather than Gradle's output.

// writeFile creates a file with n bytes of deterministic content, making its
// parent directories as needed.
func writeFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	body := strings.Repeat("x", n)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// mkdir creates a directory and every parent it needs.
func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
}

// buildTree writes the standard fixture tree into a fresh temporary
// directory and returns its root.
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// api: an ordinary Node project, with another project vendored inside its
	// node_modules. The inner one must never be listed on its own.
	writeFile(t, filepath.Join(root, "api", "package.json"), 40)
	writeFile(t, filepath.Join(root, "api", "src", "index.js"), 100)
	writeFile(t, filepath.Join(root, "api", "node_modules", "left-pad", "index.js"), 1000)
	writeFile(t, filepath.Join(root, "api", "node_modules", "vendored", "package.json"), 40)
	writeFile(t, filepath.Join(root, "api", "node_modules", "vendored", "node_modules", "dep", "i.js"), 500)
	writeFile(t, filepath.Join(root, "api", "node_modules", "vendored", "dist", "bundle.js"), 500)

	// web: node_modules plus build output that a package.json vouches for.
	writeFile(t, filepath.Join(root, "web", "package.json"), 40)
	writeFile(t, filepath.Join(root, "web", "node_modules", "react", "index.js"), 2000)
	writeFile(t, filepath.Join(root, "web", "dist", "bundle.js"), 300)

	// caseweb: the same build output under a differently cased name. Windows
	// cannot hold "dist" and "Dist" side by side, so the case duplicate lives
	// in its own project.
	writeFile(t, filepath.Join(root, "caseweb", "package.json"), 40)
	writeFile(t, filepath.Join(root, "caseweb", "Dist", "bundle.js"), 300)

	// orphan: node_modules with no package.json beside it. Listed, but
	// unverified and never pre-ticked.
	writeFile(t, filepath.Join(root, "orphan", "node_modules", "whatever", "index.js"), 700)

	// rust and dotnet: marker-gated build output.
	writeFile(t, filepath.Join(root, "rust", "Cargo.toml"), 40)
	writeFile(t, filepath.Join(root, "rust", "target", "debug", "app.exe"), 1500)
	writeFile(t, filepath.Join(root, "dotnet", "App.csproj"), 40)
	writeFile(t, filepath.Join(root, "dotnet", "bin", "Debug", "app.dll"), 800)
	writeFile(t, filepath.Join(root, "dotnet", "obj", "project.assets.json"), 200)

	// notaproject: folders with junk names and no marker beside them. This is
	// somebody's source code and must be left completely alone.
	writeFile(t, filepath.Join(root, "notaproject", "build", "important.txt"), 100)
	writeFile(t, filepath.Join(root, "notaproject", "bin", "run.sh"), 100)
	writeFile(t, filepath.Join(root, "notaproject", "target", "notes.md"), 100)

	// secret: a real project, used as the never-touch subject.
	writeFile(t, filepath.Join(root, "secret", "package.json"), 40)
	writeFile(t, filepath.Join(root, "secret", "node_modules", "dep", "index.js"), 900)

	// A unicode and emoji project name.
	writeFile(t, filepath.Join(root, "проект-🚀", "package.json"), 40)
	writeFile(t, filepath.Join(root, "проект-🚀", "node_modules", "dep", "index.js"), 600)

	// A .git directory, which the walker must never descend into.
	writeFile(t, filepath.Join(root, "api", ".git", "index"), 50)
	writeFile(t, filepath.Join(root, "api", ".git", "node_modules", "decoy", "x.js"), 50)

	return root
}

// deepPath returns a path more than 260 characters long under root, and
// reports whether it could be created. Long-path support is not universal:
// where it is missing the caller skips rather than fails.
func deepPath(t *testing.T, root string) (string, bool) {
	t.Helper()
	segment := strings.Repeat("a", 30)
	deep := root
	for len(deep) < 300 {
		deep = filepath.Join(deep, segment)
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		return "", false
	}
	return deep, true
}

// makeJunction creates a directory junction at link pointing at target,
// reporting whether it worked. Go has no portable way to create one, and
// mklink is the documented way to do it on Windows without elevation, so the
// tests shell out here and nowhere else.
//
//nolint:unused // used only by the Windows-only tests in this package
func makeJunction(t *testing.T, link, target string) bool {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, link); err != nil {
			return false
		}
		return true
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("mklink /J %s %s: %v: %s", link, target, err, out)
		return false
	}
	return true
}
