// Package templates embeds the default route templates.
package templates

import "embed"

//go:embed *.yaml
var Defaults embed.FS
