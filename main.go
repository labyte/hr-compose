package main

import (
	"fmt"
	"os"

	"hr.compose/internal/cli"
)

// version 由构建时通过 -ldflags 注入，如 make build / goreleaser。
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
