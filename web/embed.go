// Package web embeds Pouch's static frontend assets.
package web

import "embed"

//go:embed all:static
var Static embed.FS
