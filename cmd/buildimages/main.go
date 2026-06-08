package main

import (
	"context"
	"flag"
	"log"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tpsawant027/runboxd/internal/registry"
	"golang.org/x/sync/errgroup"
)

func main() {
	var dir string
	var registryPath string
	var noCache bool

	flag.StringVar(&dir, "dir", "images", "directory containing per-language image subdirs")
	flag.StringVar(&registryPath, "registry", "language_registry.yml", "path to registry YAML")
	flag.BoolVar(&noCache, "no-cache", false, "pass `--no-cache` to docker build")

	flag.Parse()

	registry, err := registry.Load(registryPath)
	if err != nil {
		log.Fatalf("failed to load registry: %v", err)
	}

	g, gctx := errgroup.WithContext(context.Background())

	for _, entry := range registry.Languages {
		for _, version := range entry.Versions {
			g.Go(func() error {
				startTime := time.Now()
				log.Printf("building image for %s %s", entry.Name, version.Name)
				imageTag := version.Image
				buildDir := filepath.Join(dir, entry.Name, version.Name)
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
		log.Fatalf("failed to build all images: %v", err)
	}
}
