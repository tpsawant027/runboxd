package images

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "images",
		Short: "Manage sandbox language images (build, lock, export)",
	}
	c.PersistentFlags().String("image-dir", "images", "directory containing per-language image build contexts")
	c.PersistentFlags().String("registry", "language_registry.yml", "path to the language registry file")
	c.AddCommand(
		newGenLockCmd(), newGenImagesCmd(), newBuildImagesCmd(),
		newExportRootFSCmd(),
	)
	return c
}
