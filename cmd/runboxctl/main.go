package main

import (
	"os"

	"github.com/tpsawant027/runboxd/cmd/runboxctl/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
