# internal/shots — screenshot renderer

Renders real screen frames for the documentation site with every I/O call
faked: the app model is driven by the same key presses a person would make, and
each frame is what the screens draw. All files carry the `shots` build tag, so a
plain `go build`, `go test` and `golangci-lint run` ignore them.

    task docs:shots                         # frames and images, end to end
    go vet -tags shots ./internal/shots
    DEVPIT_SHOTS=1 go test -tags shots ./internal/shots -run TestRender

Environment: `DEVPIT_SHOTS_OUT` (frame folder), `DEVPIT_SHOTS_ONLY` (comma list
of names), `DEVPIT_SHOTS_TEXT=1` (print each frame to the test log).

To add a shot: add the entry to `web/docs-site/shots.json` (docs pages) or
`shots.content.json` (blog, troubleshooting), and if its `screen/state` is new,
one line in `registry_test.go`. The image step is
`web/docs-site/scripts/shots-render.mjs`; images go to
`web/docs-site/src/assets/screens/<name>.webp` and are committed.
