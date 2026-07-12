package images

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/internal/imagespec"
)

func contextHash(buildDir string) (string, error) {
	hash := sha256.New()
	filesToHash := []string{"Dockerfile", "wrapper.sh"}
	for _, file := range filesToHash {
		data, err := os.ReadFile(filepath.Join(buildDir, file))
		if err != nil {
			return "", fmt.Errorf("failed to read file %s: %w", file, err)
		}
		hash.Write(data)
	}
	hexHash := hex.EncodeToString(hash.Sum(nil))
	return hexHash, nil
}

func mustGetFlagString(cmd *cobra.Command, name string) string {
	v, err := cmd.Flags().GetString(name)
	if err != nil {
		panic(fmt.Sprintf("flag %q: %v", name, err))
	}
	return v
}

func mustGetFlagStringArray(cmd *cobra.Command, name string) []string {
	v, err := cmd.Flags().GetStringArray(name)
	if err != nil {
		panic(fmt.Sprintf("flag %q: %v", name, err))
	}
	return v
}

func mustGetFlagBool(cmd *cobra.Command, name string) bool {
	v, err := cmd.Flags().GetBool(name)
	if err != nil {
		panic(fmt.Sprintf("flag %q: %v", name, err))
	}
	return v
}

func loadLangFilter(cmd *cobra.Command, ignoreVersions bool) (imagespec.LangFilter, error) {
	raw := mustGetFlagStringArray(cmd, "lang")
	if len(raw) == 0 {
		return nil, nil
	}
	return imagespec.ParseLangFilter(raw, imagespec.ParseLangFilterOptions{IgnoreVersions: ignoreVersions})
}
