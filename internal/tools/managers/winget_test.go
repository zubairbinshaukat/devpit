package managers_test

import (
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

func TestWingetCommands(t *testing.T) {
	w := managers.Winget{}
	if w.Name() != "winget" {
		t.Errorf("Name() = %q", w.Name())
	}
	steps := w.UpgradeAllCmds()
	if len(steps) != 1 {
		t.Fatalf("UpgradeAllCmds() has %d steps, want 1", len(steps))
	}
	install := w.InstallCmd("Git.Git")
	found := false
	for _, a := range install {
		if a == "Git.Git" {
			found = true
		}
	}
	if !found {
		t.Errorf("InstallCmd(Git.Git) = %v, id missing", install)
	}
	if w.NeedsElevation() {
		t.Error("NeedsElevation() = true, want false")
	}
}

func TestWingetParseList(t *testing.T) {
	w := managers.Winget{}
	out := readTestdata(t, "winget_list.txt")
	got := w.ParseList(out)
	want := []managers.Installed{
		{Name: "7-Zip", ID: "7zip.7zip", Version: "23.01"},
		{Name: "Git", ID: "Git.Git", Version: "2.43.0"},
		{Name: "Microsoft Edge", ID: "Microsoft.Edge", Version: "120.0.0.0"},
		{Name: "Visual Studio Code", ID: "Microsoft.VSCode", Version: "1.85.1"},
	}
	assertInstalledEqual(t, got, want)
}

func TestWingetParseListNone(t *testing.T) {
	w := managers.Winget{}
	out := readTestdata(t, "winget_list_none.txt")
	got := w.ParseList(out)
	if len(got) != 0 {
		t.Errorf("ParseList(none) = %v, want none", got)
	}
}

func TestWingetParseOutdated(t *testing.T) {
	w := managers.Winget{}
	out := readTestdata(t, "winget_upgrade.txt")
	got := w.ParseOutdated(out)
	want := []managers.Outdated{
		{Name: "Git", ID: "Git.Git", Current: "2.42.0", Latest: "2.43.0"},
		{Name: "Microsoft Edge", ID: "Microsoft.Edge", Current: "120.0.0.0", Latest: "121.0.0.0"},
	}
	assertOutdatedEqual(t, got, want)
}

func TestWingetParseOutdatedNone(t *testing.T) {
	w := managers.Winget{}
	out := readTestdata(t, "winget_upgrade_none.txt")
	got := w.ParseOutdated(out)
	if len(got) != 0 {
		t.Errorf("ParseOutdated(none) = %v, want none", got)
	}
}

func TestWingetUpgradeCommands(t *testing.T) {
	w := managers.Winget{}
	want := []string{
		"winget", "upgrade", "--id", "Git.Git", "-e", "--silent",
		"--accept-package-agreements", "--accept-source-agreements",
		"--disable-interactivity",
	}
	assertArgv(t, "UpgradeCmd", w.UpgradeCmd("Git.Git"), want)
	check := w.CheckCmds()
	if len(check) != 1 {
		t.Fatalf("CheckCmds() = %v, want one step", check)
	}
	assertArgv(t, "CheckCmds()[0]", check[0], w.OutdatedCmd())
	if got := w.CleanupCmds([]string{"Git.Git"}); got != nil {
		t.Errorf("CleanupCmds() = %v, want nil", got)
	}
}

func TestWingetParseOutdatedReport(t *testing.T) {
	tests := []struct {
		file    string
		want    []managers.Outdated
		unknown int
		pinned  int
	}{
		{
			file: "winget_upgrade_en.txt",
			want: []managers.Outdated{
				{Name: "Claude", ID: "Anthropic.Claude", Current: "1.21459.0.0", Latest: "1.44121.2"},
				{Name: "Docker Desktop", ID: "Docker.DockerDesktop", Current: "4.87.0", Latest: "4.91.0"},
				{Name: "Microsoft Visual C++ 2015-2022 Redistributable (x86) - 14.44.35211", ID: "Microsoft.VCRedist.2015+.x86", Current: "14.44.35211.0", Latest: "14.51.36247.0"},
				{Name: "Recordly 1.3.3", ID: "Webadderall.Recordly", Current: "1.3.3", Latest: "v1.4.0"},
			},
			unknown: 1,
		},
		{
			file: "winget_upgrade_it.txt",
			want: []managers.Outdated{
				{Name: "Git", ID: "Git.Git", Current: "2.42.0", Latest: "2.43.0"},
				{Name: "Docker Desktop", ID: "Docker.DockerDesktop", Current: "4.87.0", Latest: "4.91.0"},
			},
		},
		{
			file: "winget_upgrade_zh.txt",
			want: []managers.Outdated{
				{Name: "微信", ID: "Tencent.WeChat", Current: "3.9.8.25", Latest: "3.9.12.17"},
				{Name: "Docker Desktop", ID: "Docker.DockerDesktop", Current: "4.87.0", Latest: "4.91.0"},
				{Name: "腾讯会议 会议", ID: "Tencent.TencentMeeting", Current: "3.20.1", Latest: "3.21.0"},
			},
		},
		{
			// Korean's "사용 가능" (Available) is two words: the header
			// tokenizes into six, and the split must be undone.
			file: "winget_upgrade_ko.txt",
			want: []managers.Outdated{
				{Name: "Git", ID: "Git.Git", Current: "2.42.0", Latest: "2.43.0"},
				{Name: "카카오톡", ID: "Kakao.KakaoTalk", Current: "3.5.0.3383", Latest: "3.6.1.3500"},
			},
		},
		{
			file: "winget_upgrade_explicit.txt",
			want: []managers.Outdated{
				{Name: "Git", ID: "Git.Git", Current: "2.42.0", Latest: "2.43.0"},
				{Name: "Microsoft Edge", ID: "Microsoft.Edge", Current: "120.0.0.0", Latest: "121.0.0.0"},
				{Name: "Discord", ID: "Discord.Discord", Current: "1.0.9030", Latest: "1.0.9170", Explicit: true},
			},
			unknown: 1,
		},
		{
			file:   "winget_upgrade_pinned.txt",
			want:   []managers.Outdated{{Name: "Git", ID: "Git.Git", Current: "2.42.0", Latest: "2.43.0"}},
			pinned: 2,
		},
		{
			file: "winget_upgrade.txt",
			want: []managers.Outdated{
				{Name: "Git", ID: "Git.Git", Current: "2.42.0", Latest: "2.43.0"},
				{Name: "Microsoft Edge", ID: "Microsoft.Edge", Current: "120.0.0.0", Latest: "121.0.0.0"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			out := readTestdata(t, tt.file)
			rep := managers.Winget{}.ParseOutdatedReport(out)
			if !rep.Parsed {
				t.Fatal("Parsed = false, want true")
			}
			assertOutdatedEqual(t, rep.Packages, tt.want)
			if rep.Unknown != tt.unknown || rep.Pinned != tt.pinned {
				t.Errorf("Unknown, Pinned = %d, %d; want %d, %d", rep.Unknown, rep.Pinned, tt.unknown, tt.pinned)
			}

			// Real winget output, as captured from a pipe: CRLF line ends
			// and the spinner redrawn with bare \r on the header's line.
			// (The fixtures are stored LF-only, since .gitattributes
			// normalises line endings.)
			rawWinget := "   - \r   \\ \r   | \r" + strings.ReplaceAll(out, "\n", "\r\n")
			raw := managers.Winget{}.ParseOutdatedReport(rawWinget)
			assertOutdatedEqual(t, raw.Packages, tt.want)
			if raw.Unknown != tt.unknown || raw.Pinned != tt.pinned {
				t.Errorf("raw: Unknown, Pinned = %d, %d; want %d, %d", raw.Unknown, raw.Pinned, tt.unknown, tt.pinned)
			}

			// The Update screen rebuilds a check step's output from the
			// cleaned lines tools.RunStepLines delivers; the table must
			// survive that trip.
			var cleaned []string
			for _, line := range strings.Split(rawWinget, "\n") {
				if c := tools.CleanLine(line); c != "" {
					cleaned = append(cleaned, c)
				}
			}
			again := managers.Winget{}.ParseOutdatedReport(strings.Join(cleaned, "\n"))
			assertOutdatedEqual(t, again.Packages, tt.want)
		})
	}
}

func TestWingetParseOutdatedReportNone(t *testing.T) {
	rep := managers.Winget{}.ParseOutdatedReport(readTestdata(t, "winget_upgrade_none.txt"))
	if !rep.Parsed || len(rep.Packages) != 0 {
		t.Errorf("report = %+v, want Parsed with no packages", rep)
	}
}

func TestWingetParseOutdatedReportUnrecognised(t *testing.T) {
	for _, out := range []string{
		"",
		"Failed in attempting to update the source: winget",
		"Nessun aggiornamento applicabile trovato.", //nolint:misspell // Italian winget output, spelled as winget prints it
	} {
		if rep := (managers.Winget{}).ParseOutdatedReport(out); rep.Parsed {
			t.Errorf("ParseOutdatedReport(%q).Parsed = true, want false", out)
		}
	}
}

func TestWingetParseOutdatedReportHavePinsFooter(t *testing.T) {
	out := "No available upgrade found.\n3 package(s) have pins that prevent upgrade. Use the 'winget pin' command to view and edit pins.\n"
	rep := managers.Winget{}.ParseOutdatedReport(out)
	if !rep.Parsed || rep.Pinned != 3 || len(rep.Packages) != 0 {
		t.Errorf("report = %+v, want Parsed, Pinned 3, no packages", rep)
	}
}

func TestWingetParseOutdatedReportOverflowingRow(t *testing.T) {
	// winget sizes its columns from the first rows it sees; a later, wider
	// Name pushes the rest of its row right. Such a row is read from the
	// right instead.
	out := strings.Join([]string{
		"Name           Id             Version  Available Source",
		"-------------------------------------------------------",
		"Git            Git.Git        2.42.0   2.43.0    winget",
		"A Very Long Application Name Vendor.LongApp 1.0.0 1.1.0 winget",
		"",
	}, "\n")
	rep := managers.Winget{}.ParseOutdatedReport(out)
	assertOutdatedEqual(t, rep.Packages, []managers.Outdated{
		{Name: "Git", ID: "Git.Git", Current: "2.42.0", Latest: "2.43.0"},
		{Name: "A Very Long Application Name", ID: "Vendor.LongApp", Current: "1.0.0", Latest: "1.1.0"},
	})
}

func TestWingetParseOutdatedReportNoSourceColumn(t *testing.T) {
	out := strings.Join([]string{
		"Name  Id       Version Available",
		"--------------------------------",
		"Git   Git.Git  2.42.0  2.43.0",
	}, "\n")
	rep := managers.Winget{}.ParseOutdatedReport(out)
	assertOutdatedEqual(t, rep.Packages, []managers.Outdated{
		{Name: "Git", ID: "Git.Git", Current: "2.42.0", Latest: "2.43.0"},
	})
}

func TestWingetParseListLocalized(t *testing.T) {
	out := strings.Join([]string{
		"名前                ID                      バージョン  ソース",
		"--------------------------------------------------------------",
		"7-Zip               7zip.7zip               23.01       winget",
		"メモ帳              Microsoft.WindowsNotepad 11.2312.18.0 msstore",
	}, "\n")
	got := managers.Winget{}.ParseList(out)
	if len(got) == 0 || got[0] != (managers.Installed{Name: "7-Zip", ID: "7zip.7zip", Version: "23.01"}) {
		t.Errorf("ParseList(ja) = %+v", got)
	}
}

func assertArgv(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

// When winget prints only the explicit-targeting table, its apps are still
// marked explicit, so they start unticked like they would beside a main one.
func TestWingetExplicitOnlyTable(t *testing.T) {
	out := "The following packages have an upgrade available, but require explicit targeting for upgrade:\n" +
		"Name    Id               Version Available Source\n" +
		"-------------------------------------------------\n" +
		"Discord Discord.Discord  1.0.9   1.0.10    winget\n"
	rep := (managers.Winget{}).ParseOutdatedReport(out)
	if len(rep.Packages) != 1 || !rep.Packages[0].Explicit {
		t.Fatalf("packages = %+v, want one explicit Discord", rep.Packages)
	}
	main := (managers.Winget{}).ParseOutdatedReport("Name    Id               Version Available Source\n" +
		"-------------------------------------------------\n" +
		"Discord Discord.Discord  1.0.9   1.0.10    winget\n")
	if len(main.Packages) != 1 || main.Packages[0].Explicit {
		t.Errorf("a plain table was marked explicit: %+v", main.Packages)
	}
}
