package images

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/internal/imagespec"
	"go.yaml.in/yaml/v4"
	"golang.org/x/sync/errgroup"
)

func newGenLockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen-lock",
		Short: "Generate a lockfile with the current digests of all base images",
		RunE:  runGenLock,
	}
	cmd.Flags().String("lockfile", "images.lock.yml", "path where the generated lockfile will be written")
	cmd.Flags().Bool("drop-stale", false, "drop entries whose digest can't be refreshed instead of keeping the existing one")
	cmd.Flags().Bool("verbose", false, "enable verbose logging")
	return cmd
}

type refreshFailure struct {
	lang    string
	version string
	kept    bool
}

func runGenLock(cmd *cobra.Command, _ []string) error {
	imageDir := mustGetFlagString(cmd, "image-dir")
	lockfileOut := mustGetFlagString(cmd, "lockfile")
	dropStale := mustGetFlagBool(cmd, "drop-stale")
	verbose := mustGetFlagBool(cmd, "verbose")

	parsedLangFilter, err := loadLangFilter(cmd)
	if err != nil {
		return fmt.Errorf("failed to parse language filter: %w", err)
	}

	entries, err := imagespec.LoadFiltered(imageDir, parsedLangFilter)
	if err != nil {
		return fmt.Errorf("failed to load image specs: %w", err)
	}

	var currLockFile imagespec.Lockfile
	currLockFile, err = imagespec.LoadLockfile(lockfileOut)
	if err != nil {
		log.Printf("failed to load existing lockfile: %v\n", err)
	}

	newLockFile := make(imagespec.Lockfile)

	g, gctx := errgroup.WithContext(context.Background())
	g.SetLimit(5)

	var mu sync.Mutex
	var failures []refreshFailure

	for _, entry := range entries {
		for versionName, version := range entry.Spec.Versions {
			g.Go(func() error {
				baseImage, _, _ := strings.Cut(version.BaseImage, "@")
				if baseImage == "" {
					log.Printf("skipping %s %s: base image is empty", entry.Spec.Name, versionName)
					mu.Lock()
					failures = append(failures, refreshFailure{entry.Spec.Name, versionName, false})
					mu.Unlock()
					return nil
				}
				inspectCmd := exec.CommandContext(gctx, "docker", "buildx", "imagetools", "inspect", baseImage, "--format", "{{.Manifest.Digest}}")
				output, err := inspectCmd.Output()
				if err != nil {
					log.Printf("failed to inspect base image %s for %s %s: %v", baseImage, entry.Spec.Name, versionName, err)
					kept := false
					if !dropStale && currLockFile != nil && currLockFile[entry.Spec.Name][versionName] != "" {
						existing := currLockFile[entry.Spec.Name][versionName]
						log.Printf("keeping existing digest for %s %s: %s", entry.Spec.Name, versionName, existing)
						mu.Lock()
						if _, ok := newLockFile[entry.Spec.Name]; !ok {
							newLockFile[entry.Spec.Name] = make(map[string]string)
						}
						newLockFile[entry.Spec.Name][versionName] = existing
						mu.Unlock()
						kept = true
					}
					mu.Lock()
					failures = append(failures, refreshFailure{entry.Spec.Name, versionName, kept})
					mu.Unlock()
					return nil
				}
				newDigest := strings.TrimSpace(string(output))
				mu.Lock()
				if _, ok := newLockFile[entry.Spec.Name]; !ok {
					newLockFile[entry.Spec.Name] = make(map[string]string)
				}
				newLockFile[entry.Spec.Name][versionName] = newDigest
				mu.Unlock()

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

				return nil
			})
		}
	}

	_ = g.Wait()

	yamlBytes, err := yaml.Marshal(newLockFile)
	if err != nil {
		return fmt.Errorf("failed to marshal lockfile to YAML: %w", err)
	}

	if err := os.WriteFile(lockfileOut, yamlBytes, 0o644); err != nil {
		return fmt.Errorf("failed to write lockfile to %s: %w", lockfileOut, err)
	}

	log.Printf("wrote lockfile to %s", lockfileOut)

	if len(failures) > 0 {
		var kept, unpinned int
		log.Printf("WARNING: %d base image(s) failed to refresh:", len(failures))
		for _, f := range failures {
			if f.kept {
				kept++
				log.Printf("  - %s %s: kept existing digest", f.lang, f.version)
			} else {
				unpinned++
				log.Printf("  - %s %s: left unpinned (gen-images will fail for this entry)", f.lang, f.version)
			}
		}
		return fmt.Errorf("%d base image(s) failed to refresh (%d kept existing, %d unpinned)", len(failures), kept, unpinned)
	}

	return nil
}
