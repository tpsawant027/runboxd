package images

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/internal/imagespec"
	"github.com/tpsawant027/runboxd/internal/langtest"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate every language's image.yml and tests.yml without building anything",
		RunE:  runValidate,
	}
}

func runValidate(cmd *cobra.Command, _ []string) error {
	imageDir := mustGetFlagString(cmd, "image-dir")

	filter, err := loadLangFilter(cmd, true)
	if err != nil {
		return fmt.Errorf("failed to parse language filter: %w", err)
	}

	entries, specErr := imagespec.LoadFiltered(imageDir, filter)
	fixtures, fixtureErr := langtest.LoadFiltered(imageDir, filter)

	if specErr != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), specErr)
	}
	if fixtureErr != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), fixtureErr)
	}
	if specErr != nil || fixtureErr != nil {
		return fmt.Errorf("validation failed")
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%d language(s), %d fixture(s) OK\n", len(entries), len(fixtures))
	return nil
}
