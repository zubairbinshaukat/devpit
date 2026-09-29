package managers

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestHeaderStartsCountsDisplayCells(t *testing.T) {
	// 名称 is two double-width characters: four cells, six bytes, two
	// runes. ID must start at cell 6, whatever the byte or rune offset.
	got := headerStarts("名称  ID  版本  可用  源")
	want := []int{0, 6, 10, 16, 22}
	if !slices.Equal(got, want) {
		t.Errorf("headerStarts = %v, want %v", got, want)
	}
}

func TestCutCells(t *testing.T) {
	tests := []struct {
		s        string
		from, to int
		want     string
	}{
		{"abc def", 0, 4, "abc "},
		{"abc def", 4, -1, "def"},
		{"微信  Tencent.WeChat", 0, 6, "微信  "},
		{"微信  Tencent.WeChat", 6, -1, "Tencent.WeChat"},
		{"微信  Tencent.WeChat", 1, 3, "信"}, // 微 starts at cell 0, outside [1,3)
		{"short", 10, 20, ""},
	}
	for _, tt := range tests {
		if got := cutCells(tt.s, tt.from, tt.to); got != tt.want {
			t.Errorf("cutCells(%q, %d, %d) = %q, want %q", tt.s, tt.from, tt.to, got, tt.want)
		}
	}
}

func TestCJKRowsSliceWithoutFallback(t *testing.T) {
	// Every row of the Chinese fixture is aligned to its header in display
	// cells, so none may need the right-anchored fallback: needing it would
	// mean the cells were measured wrong.
	lines := normalizeWingetLines(mustRead(t, "testdata/winget_upgrade_zh.txt"))
	starts := headerStarts(lines[0])
	for _, row := range lines[2:5] {
		if misaligned(row, starts) {
			t.Errorf("row %q misaligned against starts %v", row, starts)
		}
	}
}

func TestIsSeparatorLine(t *testing.T) {
	for line, want := range map[string]bool{
		"----------":                  true,
		"  ---------------------  ":   true,
		"---------":                   false,
		"----      -------":           false,
		"":                            false,
		"-- not a separator --------": false,
	} {
		if got := isSeparatorLine(line); got != want {
			t.Errorf("isSeparatorLine(%q) = %v, want %v", line, got, want)
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}
