# internal/shots — screenshot renderer (work in progress)

Renders real screen frames for the documentation site with every I/O call
faked. All test files carry the `shots` build tag, so a plain
`go build`, `go test` and `golangci-lint run` ignore them.

Status: the scene registry and fakes exist, but the ANSI-to-image step
(`web/docs-site/scripts`) and the generated images were not finished. Some
helpers are still unused, which is why the tag is there. Run it with:

    go vet -tags shots ./internal/shots
    DEVPIT_SHOTS=1 go test -tags shots ./internal/shots -run TestRender

Images go to `web/docs-site/src/assets/screens/<name>.webp`, listed in
`web/docs-site/shots.json`.
