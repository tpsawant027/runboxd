package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/cmd/runboxctl/cmd/images"
)

var rootCmd = &cobra.Command{
	Use:   "runboxctl",
	Short: "A CLI tool for managing runboxd's sandbox language images",
}

func Execute() error {
	rootCmd.AddCommand(images.NewCmd())
	rootCmd.SilenceUsage = true
	return rootCmd.Execute()
}
