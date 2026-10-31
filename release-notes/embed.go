// Package notesdata embeds release-notes/releases.json so the binary
// carries its own release notes. It holds data only; parse it with
// internal/releasenotes. It lives beside the JSON because go:embed
// cannot reach files outside its own directory.
package notesdata

import _ "embed"

// ReleasesJSON is the raw contents of release-notes/releases.json.
//
//go:embed releases.json
var ReleasesJSON []byte
