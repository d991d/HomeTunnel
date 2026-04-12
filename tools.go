//go:build tools

// tools.go pins build-time dependencies that are not imported by source files
// but are required by the build system (gomobile, etc.).
// This prevents `go mod tidy` from removing them from go.mod.

package tools

import _ "golang.org/x/mobile/cmd/gomobile"
