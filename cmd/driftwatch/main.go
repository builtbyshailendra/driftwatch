// Command driftwatch detects Terraform drift from a plan and reports it as
// Markdown, JSON, a GitHub issue and a Slack message.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/builtbyshailendra/driftwatch/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...". Plain
// "go build" and "go install" leave it empty, and the version then comes from
// the module information the Go toolchain embeds in the binary.
var version string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.NewApp(cli.ResolveVersion(version, debug.ReadBuildInfo)).Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
