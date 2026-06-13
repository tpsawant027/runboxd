package cmd

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/internal/imagespec"
	"go.yaml.in/yaml/v4"
)

var genlockCmd = &cobra.Command{
	Use:   "gen-lock",
	Short: "Generate a lockfile with the current digests of all base images",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGenLock(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(genlockCmd)

	genlockCmd.Flags().String("lockfile", "images.lock.yml", "path where the generated lockfile will be written")
	genlockCmd.Flags().Bool("verbose", false, "enable verbose logging")
}

func runGenLock(cmd *cobra.Command, _ []string) error {
	imageDir, err := cmd.Flags().GetString("image-dir")
	if err != nil {
		return fmt.Errorf("failed to get flag: %w", err)
	}
	lockfileOut, err := cmd.Flags().GetString("lockfile")
	if err != nil {
		return fmt.Errorf("failed to get flag: %w", err)
	}
	verbose, _ := cmd.Flags().GetBool("verbose")

	entries, err := imagespec.Load(imageDir)
	if err != nil {
		return fmt.Errorf("failed to load image specs: %w", err)
	}

	var currLockFile imagespec.Lockfile
	currLockFile, err = imagespec.LoadLockfile(lockfileOut)
	if err != nil {
		log.Printf("failed to load existing lockfile: %v\n", err)
	}

	newLockFile := make(imagespec.Lockfile)
	for _, entry := range entries {
		newLockFile[entry.Spec.Name] = make(map[string]string)
		for versionName, version := range entry.Spec.Versions {
			baseImage, _, _ := strings.Cut(version.BaseImage, "@")
			if baseImage == "" {
				log.Printf("skipping %s %s: base image is empty", entry.Spec.Name, versionName)
				continue
			}
			inspectCmd := exec.Command("docker", "buildx", "imagetools", "inspect", baseImage, "--format", "{{.Manifest.Digest}}")

			output, err := inspectCmd.Output()
			if err != nil {
				log.Printf("failed to inspect base image %s for %s %s: %v", baseImage, entry.Spec.Name, versionName, err)
				continue
			}

			newDigest := strings.TrimSpace(string(output))

			newLockFile[entry.Spec.Name][versionName] = newDigest
			if verbose {
				var oldDigest string
				if currLockFile != nil {
					oldDigest = currLockFile[entry.Spec.Name][versionName]
				}
				if oldDigest == "" {
					log.Printf("%s %s: base image digest is %s", entry.Spec.Name, versionName, newDigest)
				} else if oldDigest != newDigest {
					log.Printf("%s %s: base image digest changed from %s to %s", entry.Spec.Name, versionName, oldDigest, newDigest)
				} else {
					log.Printf("%s %s: base image digest is unchanged (%s)", entry.Spec.Name, versionName, newDigest)
				}
			}
		}

	}

	yamlBytes, err := yaml.Marshal(newLockFile)
	if err != nil {
		return fmt.Errorf("failed to marshal lockfile to YAML: %w", err)
	}

	if err := os.WriteFile(lockfileOut, yamlBytes, 0o644); err != nil {
		return fmt.Errorf("failed to write lockfile to %s: %w", lockfileOut, err)
	}

	log.Printf("lockfile generated successfully at %s", lockfileOut)

	return nil
}
