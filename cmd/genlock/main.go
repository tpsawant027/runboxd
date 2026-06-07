package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/tpsawant027/runboxd/internal/imagespec"
	"go.yaml.in/yaml/v4"
)

func main() {
	var dir string
	var out string
	var verbose bool

	flag.StringVar(&dir, "dir", "images", "directory to read image specifications from")
	flag.StringVar(&out, "out", "images.lock.yml", "output file for the generated lockfile")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging")
	flag.Parse()

	entries, err := imagespec.Load(dir)
	if err != nil {
		log.Fatalf("failed to load image specs: %v", err)
	}

	var currLockFile imagespec.Lockfile
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		currLockFile, err = imagespec.LoadLockfile(out)
		if err != nil {
			log.Printf("failed to load existing lockfile: %v", err)
		}
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
			cmd := exec.Command("docker", "buildx", "imagetools", "inspect", baseImage, "--format", "{{.Manifest.Digest}}")

			output, err := cmd.Output()
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
		log.Fatalf("failed to marshal lockfile to YAML: %v", err)
	}

	if err := os.WriteFile(out, yamlBytes, 0o644); err != nil {
		log.Fatalf("failed to write lockfile to %s: %v", out, err)
	}

	log.Printf("lockfile generated successfully at %s", out)
}
