// Command crocs is a fast-primitives CLI for AI-driven code exploration. See
// PLAN.md for the design and skills/SKILL.md for the consumer pattern.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// version is set by ldflags at release time. "dev" in source builds.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "crocs",
	Short: "Fast primitives for AI-driven code exploration",
	Long: `crocs fetches and tracks git repositories so disposable subagents can
grep, read, and reason over them without bloating the parent agent's context.

JSON is the default output format on every command except read-files
(XML envelope). The _meta envelope carries the schema version: branch on
_meta.crocs for forward-compatible scripting.`,
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "crocs:", err)
		os.Exit(1)
	}
}
