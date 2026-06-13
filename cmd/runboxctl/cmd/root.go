package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "runboxctl",
	Short: "A CLI tool for managing runboxd's sandbox language images",
}

func Execute() error {
	rootCmd.SilenceUsage = true
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().String("image-dir", "images", "directory containing per-language image specifications and where generated Dockerfiles will be written")
	rootCmd.PersistentFlags().String("registry", "language_registry.yml", "path to the language registry file")
}
