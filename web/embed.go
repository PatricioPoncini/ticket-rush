// Package web holds the static files of the demo UI.
package web

import "embed"

// Files is the UI: index.html, app.js and style.css.
//
//go:embed index.html app.js style.css
var Files embed.FS
