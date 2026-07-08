package images

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/internal/imagespec"
	"github.com/tpsawant027/runboxd/internal/langtest"
)

func newListImagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all available images",
		RunE:  runListImages,
	}
	return cmd
}

func runListImages(cmd *cobra.Command, _ []string) error {
	imageDir := mustGetFlagString(cmd, "image-dir")

	parsedLangFilter, err := loadLangFilter(cmd)
	if err != nil {
		return fmt.Errorf("failed to parse language filter: %w", err)
	}

	entries, err := imagespec.LoadFiltered(imageDir, parsedLangFilter)
	if err != nil {
		return fmt.Errorf("failed to load image specs: %w", err)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "LANGUAGE\tVERSION\tDEFAULT\tTYPE\tTESTS\tBASE IMAGE")
	for _, entry := range entries {
		testsExist := "no"
		if _, err := os.Stat(filepath.Join(entry.Dir, langtest.FixtureFilename)); err == nil {
			testsExist = "yes"
		}
		versionNames := slices.Sorted(maps.Keys(entry.Spec.Versions))
		for _, versionName := range versionNames {
			version := entry.Spec.Versions[versionName]
			defaultStr := ""
			if entry.Spec.DefaultVersion == versionName {
				defaultStr = "*"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", entry.Spec.Name, versionName, defaultStr, entry.Spec.Type, testsExist, version.BaseImage)
		}
	}
	w.Flush()
	return nil
}
