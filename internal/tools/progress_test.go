package tools_test

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

func TestParseProgress(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want tools.Progress
		ok   bool
	}{
		{
			name: "percent",
			in:   "Downloading 42%",
			want: tools.Progress{Percent: 42},
			ok:   true,
		},
		{
			name: "decimal percent with space",
			in:   "  42.5 %",
			want: tools.Progress{Percent: 42.5},
			ok:   true,
		},
		{
			name: "percent clamped",
			in:   "105%",
			want: tools.Progress{Percent: 100},
			ok:   true,
		},
		{
			name: "winget bar raw",
			in:   "  ██████▒▒▒▒▒▒  38.1 MB / 90.2 MB\r",
			want: tools.Progress{Percent: 38.1 / 90.2 * 100, Done: "38.1 MB", Total: "90.2 MB"},
			ok:   true,
		},
		{
			name: "sizes across units",
			in:   "512 KB / 2 MB",
			want: tools.Progress{Percent: 25, Done: "512 KB", Total: "2 MB"},
			ok:   true,
		},
		{
			name: "binary units",
			in:   "1 GiB / 4 GiB",
			want: tools.Progress{Percent: 25, Done: "1 GiB", Total: "4 GiB"},
			ok:   true,
		},
		{
			name: "bytes",
			in:   "50 B / 200 B",
			want: tools.Progress{Percent: 25, Done: "50 B", Total: "200 B"},
			ok:   true,
		},
		{
			name: "explicit percent wins over sizes",
			in:   "12.5 MB / 105 MB  10%",
			want: tools.Progress{Percent: 10, Done: "12.5 MB", Total: "105 MB"},
			ok:   true,
		},
		{
			name: "zero total",
			in:   "0 B / 0 B",
			want: tools.Progress{Percent: -1, Done: "0 B", Total: "0 B"},
			ok:   true,
		},
		{
			name: "winget found with version",
			in:   "(3/15) Found Docker Desktop [Docker.DockerDesktop] Version 4.91.0",
			want: tools.Progress{Percent: -1, Index: 3, Count: 15, Name: "Docker Desktop", ID: "Docker.DockerDesktop"},
			ok:   true,
		},
		{
			name: "winget found without version",
			in:   "(1/2) Found Git [Git.Git]",
			want: tools.Progress{Percent: -1, Index: 1, Count: 2, Name: "Git", ID: "Git.Git"},
			ok:   true,
		},
		{
			name: "localized counter only",
			in:   "(2/4) Trovato Git [Git.Git] Versione 2.43.0",
			want: tools.Progress{Percent: -1, Index: 2, Count: 4},
			ok:   true,
		},
		{
			name: "plain log line",
			in:   "Successfully installed",
			want: tools.Progress{Percent: -1},
			ok:   false,
		},
		{
			name: "empty",
			in:   "",
			want: tools.Progress{Percent: -1},
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tools.ParseProgress(tt.in)
			if ok != tt.ok {
				t.Errorf("ok = %v, want %v", ok, tt.ok)
			}
			if !closeEnough(got.Percent, tt.want.Percent) {
				t.Errorf("Percent = %v, want %v", got.Percent, tt.want.Percent)
			}
			got.Percent, tt.want.Percent = 0, 0
			if got != tt.want {
				t.Errorf("ParseProgress(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func closeEnough(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}
