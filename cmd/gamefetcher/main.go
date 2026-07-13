package main

import (
	"os"

	"github.com/Austrum-lab/game-fetcher-cli/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
