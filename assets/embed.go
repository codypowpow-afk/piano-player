// Package assets embeds the instrument sample sets built by tools/samples/build.py.
package assets

import "embed"

//go:embed */index.json */*.ogg
var FS embed.FS
