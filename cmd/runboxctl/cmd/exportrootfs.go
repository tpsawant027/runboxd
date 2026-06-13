package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/internal/registry"
	"golang.org/x/sync/errgroup"
)

var exportRootFSCmd = &cobra.Command{
	Use:   "export-rootfs",
	Short: "Export the root filesystem of a built image as a directory",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runExportRootFS(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(exportRootFSCmd)

	exportRootFSCmd.Flags().String("rootfs-dir", "_rootfs", "directory where the exported root filesystem tarballs will be written")
	exportRootFSCmd.Flags().Bool("force", false, "re-export all rootfs even if the image digest is unchanged")
}

func runExportRootFS(cmd *cobra.Command, _ []string) error {
	registryPath, err := cmd.Flags().GetString("registry")
	if err != nil {
		return fmt.Errorf("failed to get flag: %w", err)
	}
	rootfsDir, err := cmd.Flags().GetString("rootfs-dir")
	if err != nil {
		return fmt.Errorf("failed to get flag: %w", err)
	}
	force, _ := cmd.Flags().GetBool("force")

	registry, err := registry.Load(registryPath)
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}

	g, gctx := errgroup.WithContext(context.Background())

	for _, entry := range registry.Languages {
		for _, version := range entry.Versions {
			g.Go(func() error {
				startTime := time.Now()

				dest := filepath.Join(rootfsDir, entry.Name, version.Name)
				destDigestFile := dest + ".digest"
				imageID, err := getImageID(gctx, version.Image)
				if err != nil {
					log.Printf("failed to get image ID for %s %s: %v", entry.Name, version.Name, err)
					return err
				}
				if !force {
					existingDigest, err := os.ReadFile(destDigestFile)
					if err == nil && strings.TrimSpace(string(existingDigest)) == imageID && dirExistsAndNotEmpty(dest) {
						log.Printf("rootfs for %s %s is up to date, skipping", entry.Name, version.Name)
						return nil
					}
				}

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

				if err := os.WriteFile(destDigestFile, []byte(imageID), 0o644); err != nil {
					log.Printf("failed to write digest file for %s %s: %v", entry.Name, version.Name, err)
					return err
				}

				log.Printf("successfully exported rootfs for %s %s in %s", entry.Name, version.Name, time.Since(startTime))
				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to export all rootfs: %w", err)
	}
	return nil
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

func getImageID(ctx context.Context, image string) (string, error) {
	inspectCmd := exec.CommandContext(ctx, "docker", "image", "inspect", image, "--format", "{{.Id}}")
	output, err := inspectCmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			stderr = string(ee.Stderr)
		}
		return "", fmt.Errorf("failed to inspect image %s: %v\n%s", image, err, stderr)
	}
	return strings.TrimSpace(string(output)), nil
}

func dirExistsAndNotEmpty(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	return err == nil
}
