package adapters

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// GitSigning is how commits under one Git identity are signed. All fields
// are optional; the zero value signs nothing.
type GitSigning struct {
	// Key is user.signingkey: a GPG key id or an SSH public key's path.
	Key string `json:"signingkey,omitempty"`
	// Format is gpg.format: "openpgp", "x509" or "ssh".
	Format string `json:"format,omitempty"`
	// Sign is commit.gpgsign.
	Sign bool `json:"gpgsign,omitempty"`
}

// IsZero reports whether s sets nothing.
func (s GitSigning) IsZero() bool { return s == GitSigning{} }

func (s GitSigning) validate() error {
	switch s.Format {
	case "", "openpgp", "x509", "ssh":
	default:
		return fmt.Errorf("gpg.format %q is not one Git knows (openpgp, x509 or ssh)", s.Format)
	}
	if _, err := gitQuote(s.Key); err != nil {
		return fmt.Errorf("signing key: %w", err)
	}
	secret := accounts.LooksSecret(s.Key)
	if p, ok := accounts.IsAbsPathArg(s.Key); ok {
		secret = accounts.PathLooksSecret(p) // an SSH public key's path
	}
	if secret {
		return errors.New("the signing key looks like a secret. Use the key's id, or the path of an SSH public key, never the key itself")
	}
	return nil
}

// GitHelper records Devpit's credential helper for github.com.
type GitHelper struct {
	// Command is the helper value Devpit wrote after the reset:
	// !"C:/…/devpit.exe" git-credential.
	Command string `json:"command"`
	// Previous is the helper chain Git used for https://github.com before
	// Devpit took over, in order. The default account passes through to
	// it, and removing Devpit's helper brings it back by itself.
	Previous []string `json:"previous"`
}

// gitExtras is devpit.json: the Git settings accounts.toml has no field
// for. It is written through the engine like every other file.
type gitExtras struct {
	Version int                   `json:"version"`
	Signing map[string]GitSigning `json:"signing,omitempty"`
	// SSHKeys maps a folder (normalized) to the private key used to push
	// from it.
	SSHKeys map[string]string `json:"ssh_keys,omitempty"`
	Helper  *GitHelper        `json:"credential_helper,omitempty"`
}

func (x gitExtras) empty() bool {
	return len(x.Signing) == 0 && len(x.SSHKeys) == 0 && x.Helper == nil
}

func (x gitExtras) clone() gitExtras {
	out := gitExtras{Version: 1}
	if len(x.Signing) > 0 {
		out.Signing = map[string]GitSigning{}
		for k, v := range x.Signing {
			out.Signing[k] = v
		}
	}
	if len(x.SSHKeys) > 0 {
		out.SSHKeys = map[string]string{}
		for k, v := range x.SSHKeys {
			out.SSHKeys[k] = v
		}
	}
	if x.Helper != nil {
		h := *x.Helper
		h.Previous = slices.Clone(h.Previous)
		out.Helper = &h
	}
	return out
}

func (x gitExtras) encode() []byte {
	x.Version = 1
	b, _ := json.MarshalIndent(x, "", "  ") // plain maps and strings: cannot fail
	return append(b, '\n')
}

func readGitExtras(dir string) (gitExtras, error) {
	path := filepath.Join(dir, gitExtrasFile)
	data, err := os.ReadFile(path) // #nosec G304 -- Devpit's own file
	if errors.Is(err, os.ErrNotExist) {
		return gitExtras{Version: 1}, nil
	}
	if err != nil {
		return gitExtras{}, err
	}
	var x gitExtras
	if err := json.Unmarshal(data, &x); err != nil {
		return gitExtras{}, fmt.Errorf("%s cannot be read (%w). Fix or delete it, then try again", path, err)
	}
	if x.Version > 1 {
		return gitExtras{}, fmt.Errorf("%s was written by a newer Devpit; update Devpit", path)
	}
	return x, nil
}

// LoadGitHelper reads the credential helper Devpit set up, if any: what
// `devpit git-credential` passes the default account through to. ok is
// false when Devpit has not taken over github.com pushes.
func LoadGitHelper(gitDir string) (h GitHelper, ok bool, err error) {
	x, err := readGitExtras(gitDir)
	if err != nil || x.Helper == nil {
		return GitHelper{}, false, err
	}
	return *x.Helper, true, nil
}

// signingFor looks an identity's signing options up, ignoring case.
func (x gitExtras) signingFor(name string) GitSigning {
	return x.Signing[strings.ToLower(name)]
}
