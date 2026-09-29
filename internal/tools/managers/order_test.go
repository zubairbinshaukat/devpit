package managers_test

import (
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

func ids(pkgs []managers.Outdated) string {
	out := make([]string, len(pkgs))
	for i, p := range pkgs {
		out[i] = p.ID
	}
	return strings.Join(out, ",")
}

func outdatedIDs(list ...string) []managers.Outdated {
	pkgs := make([]managers.Outdated, len(list))
	for i, id := range list {
		pkgs[i] = managers.Outdated{Name: id, ID: id}
	}
	return pkgs
}

func TestUpgradeOrder(t *testing.T) {
	tests := []struct {
		manager string
		in      []managers.Outdated
		want    string
	}{
		{
			manager: "winget",
			in:      outdatedIDs("Microsoft.AppInstaller", "Git.Git", "Docker.DockerDesktop", "OpenJS.NodeJS.LTS", "Microsoft.WSL", "Anthropic.Claude"),
			want:    "Docker.DockerDesktop,Anthropic.Claude,Git.Git,OpenJS.NodeJS.LTS,Microsoft.WSL,Microsoft.AppInstaller",
		},
		{
			manager: "scoop",
			in:      outdatedIDs("scoop", "devpit", "nodejs-lts", "7zip", "git", "nvm", "ripgrep"),
			want:    "7zip,ripgrep,git,nodejs-lts,nvm,scoop,devpit",
		},
		{
			manager: "npm",
			in:      outdatedIDs("npm", "typescript", "pnpm"),
			want:    "typescript,pnpm,npm",
		},
		{
			manager: "choco",
			in:      outdatedIDs("chocolatey", "git.install", "vlc"),
			want:    "vlc,git.install,chocolatey",
		},
		{
			manager: "winget",
			in:      outdatedIDs("b", "a", "c"),
			want:    "b,a,c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.manager+" "+tt.want, func(t *testing.T) {
			before := ids(tt.in)
			got := managers.UpgradeOrder(tt.manager, tt.in)
			if ids(got) != tt.want {
				t.Errorf("UpgradeOrder = %s, want %s", ids(got), tt.want)
			}
			if ids(tt.in) != before {
				t.Errorf("UpgradeOrder modified its input: %s, was %s", ids(tt.in), before)
			}
		})
	}
}

func TestIsDevpit(t *testing.T) {
	for id, want := range map[string]bool{
		"devpit":                  true,
		"Devpit":                  true,
		"scoop-bucket/devpit":     true,
		"ZubairBinShaukat.Devpit": true,
		"devpitstop":              false,
		"Git.Git":                 false,
		"":                        false,
	} {
		if got := managers.IsDevpit(id); got != want {
			t.Errorf("IsDevpit(%q) = %v, want %v", id, got, want)
		}
	}
}
