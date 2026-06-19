package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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
