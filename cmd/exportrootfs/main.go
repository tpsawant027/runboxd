package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tpsawant027/runboxd/internal/registry"
	"golang.org/x/sync/errgroup"
)

func main() {
	var registryPath string
	var rootfsDir string

	flag.StringVar(&registryPath, "registry", "language_registry.yml", "path to registry YAML")
	flag.StringVar(&rootfsDir, "rootfs", "rootfs", "directory to export root filesystems to")

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
				log.Printf("exporting rootfs for %s %s", entry.Name, version.Name)

				dest := filepath.Join(rootfsDir, entry.Name, version.Name)

				if err := os.RemoveAll(dest); err != nil {
					log.Printf("failed to remove existing rootfs for %s %s: %v", entry.Name, version.Name, err)
					return err
				}
				if err := os.MkdirAll(dest, 0o755); err != nil {
					log.Printf("failed to create rootfs directory for %s %s: %v", entry.Name, version.Name, err)
					return err
				}

				createCmd := exec.CommandContext(gctx, "docker", "create", version.Image)
				output, err := createCmd.Output()
				if err != nil {
					stderr := ""
					if ee, ok := errors.AsType[*exec.ExitError](err); ok {
						stderr = string(ee.Stderr)
					}
					log.Printf("failed to create container for %s %s: %v\n%s", entry.Name, version.Name, err, stderr)
					return err
				}
				containerID := strings.TrimSpace(string(output))
				defer func() {
					rmCmd := exec.CommandContext(context.Background(), "docker", "rm", "-f", containerID)
					if out, err := rmCmd.CombinedOutput(); err != nil {
						log.Printf("failed to remove container for %s %s: %v\nOutput: %s", entry.Name, version.Name, err, string(out))
					}
				}()

				if err := exportRootfs(gctx, containerID, dest, entry, version); err != nil {
					log.Printf("failed to export rootfs for %s %s: %v", entry.Name, version.Name, err)
					return err
				}

				for _, m := range []string{"sandbox", "tmp", "build", "input"} {
					if err := os.MkdirAll(filepath.Join(dest, m), 0o755); err != nil {
						log.Printf("failed to create %s directory in rootfs for %s %s: %v", m, entry.Name, version.Name, err)
						return err
					}
				}

				log.Printf("successfully exported rootfs for %s %s in %s", entry.Name, version.Name, time.Since(startTime))
				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		log.Fatalf("failed to export all rootfs: %v", err)
	}
}

func exportRootfs(gctx context.Context, containerID string, dest string, entry registry.Language, version registry.Version) error {
	exportCmd := exec.CommandContext(gctx, "docker", "export", containerID)
	tarCmd := exec.CommandContext(gctx, "tar", "-x", "-C", dest)

	stdout, err := exportCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to pipe export stdout for %s %s: %w", entry.Name, version.Name, err)
	}
	tarCmd.Stdin = stdout

	if err := exportCmd.Start(); err != nil {
		return fmt.Errorf("failed to start export command for %s %s: %w", entry.Name, version.Name, err)
	}
	if err := tarCmd.Start(); err != nil {
		_ = exportCmd.Process.Kill()
		_ = exportCmd.Wait()
		return fmt.Errorf("failed to start tar command for %s %s: %w", entry.Name, version.Name, err)
	}
	if err := exportCmd.Wait(); err != nil {
		return fmt.Errorf("failed to export container for %s %s: %w", entry.Name, version.Name, err)
	}
	if err := tarCmd.Wait(); err != nil {
		return fmt.Errorf("failed to extract rootfs for %s %s: %w", entry.Name, version.Name, err)
	}

	return nil
}
