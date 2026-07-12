package images

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/internal/registry"
	"golang.org/x/sync/errgroup"
)

const imageCacheDir = ".image_cache"

func newBuildImagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build-images",
		Short: "Build all images using the generated Dockerfiles",
		RunE:  runBuildImages,
	}
	cmd.Flags().Bool("no-cache", false, "pass `--no-cache` to docker build")
	return cmd
}

func runBuildImages(cmd *cobra.Command, _ []string) error {
	imageDir := mustGetFlagString(cmd, "image-dir")
	registryPath := mustGetFlagString(cmd, "registry")
	noCache := mustGetFlagBool(cmd, "no-cache")

	parsedLangFilter, err := loadLangFilter(cmd, false)
	if err != nil {
		return fmt.Errorf("failed to parse language filter: %w", err)
	}

	registry, err := registry.LoadFiltered(registryPath, parsedLangFilter)
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

				hashFile := filepath.Join(imageCacheDir, entry.Name+"-"+version.Name+".hash")
				currHash, err := os.ReadFile(hashFile)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				newHash, err := contextHash(buildDir)
				if err != nil {
					log.Printf("failed to compute context hash for %s %s: %v", entry.Name, version.Name, err)
					return err
				}
				if !noCache && len(currHash) > 0 {
					if !shouldBuildImage(gctx, newHash, string(currHash), imageTag) {
						return nil
					}
				}
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
				if err := os.MkdirAll(imageCacheDir, 0o755); err != nil {
					return fmt.Errorf("failed to create image cache directory: %w", err)
				}
				if err := os.WriteFile(hashFile, []byte(newHash), 0o644); err != nil {
					return fmt.Errorf("failed to write context hash for %s %s: %w", entry.Name, version.Name, err)
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

func shouldBuildImage(ctx context.Context, newHash, currHash, imageTag string) bool {
	if newHash == currHash {
		cmd := exec.CommandContext(ctx, "docker", "inspect", imageTag)
		if err := cmd.Run(); err != nil {
			log.Printf("image %s does not exist, will build", imageTag)
			return true
		}
		log.Printf("image %s already exists and context hash matches, skipping build", imageTag)
		return false
	}
	log.Printf("context hash for image %s has changed, will build", imageTag)
	return true
}
