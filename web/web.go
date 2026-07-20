// Package web embeds the server-rendered templates and static assets so the
// final binary is fully self-contained (important for distroless images).
package web

import "embed"

// Templates holds the HTML templates.
//
//go:embed templates
var Templates embed.FS

// Static holds CSS, JS and vendored front-end libraries.
//
//go:embed static
var Static embed.FS
