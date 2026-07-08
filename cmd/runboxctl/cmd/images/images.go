package images

import "github.com/spf13/cobra"

func NewCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "images",
		Short: "Manage sandbox language images (build, lock, export, list)",
	}
	c.PersistentFlags().String("image-dir", "images", "directory containing per-language image build contexts")
	c.PersistentFlags().String("registry", "language_registry.yml", "path to the language registry file")
	c.PersistentFlags().StringArray("lang", nil, "restrict to language(s): LANG or LANG:VER[,VER,...] (repeat flag for multiple languages)")
	c.AddCommand(
		newGenLockCmd(), newGenImagesCmd(), newBuildImagesCmd(),
		newExportRootFSCmd(), newListImagesCmd(),
	)
	return c
}
