package cmd

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/internal/registry"
	"golang.org/x/sync/errgroup"
)

var buildImagesCmd = &cobra.Command{
	Use:   "build-images",
	Short: "Build all images using the generated Dockerfiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBuildImages(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(buildImagesCmd)

	buildImagesCmd.Flags().Bool("no-cache", false, "pass `--no-cache` to docker build")
}

func runBuildImages(cmd *cobra.Command, _ []string) error {
	imageDir, err := cmd.Flags().GetString("image-dir")
	if err != nil {
		return fmt.Errorf("failed to get flag: %w", err)
	}
	registryPath, err := cmd.Flags().GetString("registry")
	if err != nil {
		return fmt.Errorf("failed to get flag: %w", err)
	}
	noCache, _ := cmd.Flags().GetBool("no-cache")

	registry, err := registry.Load(registryPath)
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}

	g, gctx := errgroup.WithContext(context.Background())

	for _, entry := range registry.Languages {
		for _, version := range entry.Versions {
			g.Go(func() error {
				startTime := time.Now()

				imageTag := version.Image
				buildDir := filepath.Join(imageDir, entry.Name, version.Name)
				args := []string{"build", "-t", imageTag}
				if noCache {
					args = append(args, "--no-cache")
				}
				args = append(args, buildDir)
				cmd := exec.CommandContext(gctx, "docker", args...)
				if output, err := cmd.CombinedOutput(); err != nil {
					log.Printf("failed to build image for %s %s: %v\nOutput: %s", entry.Name, version.Name, err, string(output))
					return err
				}
				log.Printf("successfully built image for %s %s in %s", entry.Name, version.Name, time.Since(startTime))
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to build all images: %w", err)
	}
	return nil
}
