package icons

// nerdExtensions maps a lower-cased file name or extension to a Nerd Font
// glyph. The table is Devpit's own and deliberately small: about forty
// entries covering what a Windows dev machine actually contains. The idea and
// the shape of the table are inspired by lazygit's pkg/gui/presentation
// file_icons.go (MIT); see NOTICE.
//
// Every value must be a BMP private-use codepoint one cell wide. A test walks
// this map and asserts both.
var nerdExtensions = map[string]string{
	// Exact names come first; FileIcon checks them before extensions.
	"package.json":       "", // nf-dev-npm
	"package-lock.json":  "",
	"pnpm-lock.yaml":     "",
	"yarn.lock":          "",
	"node_modules":       "", // nf-dev-nodejs_small
	"dockerfile":         "", // nf-linux-docker
	"docker-compose.yml": "",
	".git":               "", // nf-dev-git
	".gitignore":         "",
	".gitattributes":     "",
	"go.mod":             "", // nf-dev-go
	"go.sum":             "",
	"cargo.toml":         "", // nf-dev-rust
	"cargo.lock":         "",
	"makefile":           "", // nf-dev-gnu
	"license":            "", // nf-fa-book
	"readme.md":          "",
	"tsconfig.json":      "", // nf-seti-typescript
	"requirements.txt":   "", // nf-dev-python
	".editorconfig":      "", // nf-seti-config
	".env":               "",

	// Extensions, with the leading dot.
	".js":     "", // nf-dev-javascript
	".mjs":    "",
	".cjs":    "",
	".jsx":    "", // nf-dev-react
	".ts":     "", // nf-seti-typescript
	".tsx":    "",
	".vue":    "", // nf-seti-vue
	".json":   "", // nf-seti-json
	".html":   "", // nf-dev-html5
	".css":    "", // nf-dev-css3
	".scss":   "", // nf-dev-sass
	".md":     "", // nf-seti-markdown
	".py":     "", // nf-dev-python
	".go":     "", // nf-dev-go
	".rs":     "", // nf-dev-rust
	".java":   "", // nf-dev-java
	".rb":     "", // nf-dev-ruby
	".php":    "", // nf-dev-php
	".cs":     "", // nf-seti-c_sharp
	".sln":    "", // nf-dev-dotnet
	".csproj": "",
	".toml":   "", // nf-seti-config
	".yml":    "",
	".yaml":   "",
	".ini":    "",
	".sql":    "", // nf-dev-database
	".db":     "",
	".sh":     "", // nf-dev-terminal
	".ps1":    "", // nf-dev-windows
	".bat":    "",
	".exe":    "",
	".dll":    "",
	".png":    "", // nf-fa-image
	".jpg":    "",
	".svg":    "",
	".gif":    "",
	".zip":    "", // nf-fa-file_archive_o
	".gz":     "",
	".7z":     "",
	".pdf":    "", // nf-fa-file_pdf_o
	".txt":    "", // nf-fa-file_text_o
	".log":    "",
	".lock":   "", // nf-fa-lock
}
