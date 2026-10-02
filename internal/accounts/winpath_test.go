package accounts

import (
	"errors"
	"testing"
)

func TestNormalizeFolder(t *testing.T) {
	cases := []struct {
		in, base, want string
		bad            bool
	}{
		{in: `C:\Work`, want: `C:\Work`},
		{in: `c:\work\`, want: `C:\work`},
		{in: `C:/Work/api/`, want: `C:\Work\api`},
		{in: `C:\Work\\api`, want: `C:\Work\api`},
		{in: `  "C:\Work"  `, want: `C:\Work`},
		{in: `\\?\C:\Work`, want: `C:\Work`},
		{in: `\??\C:\Work`, want: `C:\Work`},
		{in: `//?/C:/Work`, want: `C:\Work`},
		{in: `\\?\UNC\server\share\proj`, want: `\\server\share\proj`},
		{in: `\\server\share\proj\`, want: `\\server\share\proj`},
		{in: `\\server\share`, want: `\\server\share`},
		{in: `\\server\share\`, want: `\\server\share`},
		{in: `C:\`, want: `C:\`},
		{in: `c:`, want: `C:\`},
		{in: `C:\Work\..\..\..`, want: `C:\`},
		{in: `C:\Work\.\api\..\web`, want: `C:\Work\web`},
		{in: `C:\Work.\api `, want: `C:\Work\api`},
		{in: `api`, base: `C:\Work`, want: `C:\Work\api`},
		{in: `..\sibling`, base: `C:\Work\api`, want: `C:\Work\sibling`},
		{in: `\Temp`, base: `D:\Work`, want: `D:\Temp`},
		{in: `C:sub`, base: `C:\Work`, want: `C:\Work\sub`},
		{in: `D:sub`, base: `C:\Work`, want: `D:\sub`},
		{in: `.`, base: `\\srv\sh\x`, want: `\\srv\sh\x`},
		{in: `api`, bad: true},
		{in: ``, bad: true},
		{in: `\\server`, bad: true},
		{in: `\\.\PhysicalDrive0`, bad: true},
		{in: `C:\Wo*rk`, bad: true},
		{in: `C:\Work:stream`, bad: true},
		{in: "C:\\Wo\x01rk", bad: true},
	}
	for _, c := range cases {
		got, err := NormalizeFolder(c.in, c.base)
		if c.bad {
			if err == nil {
				t.Errorf("NormalizeFolder(%q, %q) = %q, want an error", c.in, c.base, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("NormalizeFolder(%q, %q) = %q, %v; want %q", c.in, c.base, got, err, c.want)
		}
	}
	if _, err := NormalizeFolder("x", ""); !errors.Is(err, ErrRelativePath) {
		t.Errorf("relative without base: %v", err)
	}
}

func TestFolderContains(t *testing.T) {
	cases := []struct {
		parent, child string
		want          bool
	}{
		{`C:\Work`, `C:\Work`, true},
		{`C:\Work`, `c:\work\`, true},
		{`C:\Work`, `C:\Work\api\src`, true},
		{`C:\Work`, `C:/WORK/api`, true},
		{`C:\Work\`, `C:\Work\api`, true},
		{`C:\Work`, `C:\Workshop`, false},
		{`C:\Work`, `C:\Workshop\api`, false},
		{`C:\Work\api`, `C:\Work`, false},
		{`C:\`, `C:\anything\deep`, true},
		{`C:\`, `D:\anything`, false},
		{`\\srv\share`, `\\SRV\Share\proj`, true},
		{`\\srv\share`, `\\srv\shared\proj`, false},
		{`\\?\C:\Work`, `C:\Work\x`, true},
		{`C:\Work`, `\\?\UNC\srv\share`, false},
	}
	for _, c := range cases {
		if got := FolderContains(c.parent, c.child); got != c.want {
			t.Errorf("FolderContains(%q, %q) = %v, want %v", c.parent, c.child, got, c.want)
		}
	}
	if !SameFolder(`C:\Work\`, `c:/work`) || SameFolder(`C:\Work`, `C:\Workshop`) {
		t.Error("SameFolder")
	}
	if !IsDriveRoot(`D:\`) || !IsDriveRoot(`\\srv\sh`) || IsDriveRoot(`D:\x`) {
		t.Error("IsDriveRoot")
	}
}
