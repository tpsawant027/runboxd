package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/tpsawant027/runboxd/internal/imagespec"
	"github.com/tpsawant027/runboxd/internal/registry"
	"go.yaml.in/yaml/v4"
)

type dockerfileData struct {
	BaseImage  string
	BuildCmd   string
	RunCmdJSON string
	Type       string
}

const versionEntryImageNamePrefix = "runboxd-"

//go:embed templates/dockerfile.tmpl
var dockerfileTemplate string

func main() {
	var dir string
	var force bool
	var lockfilePath string
	var registryOut string

	flag.StringVar(&dir, "dir", "images", "directory to read image specifications and write Dockerfiles to")
	flag.StringVar(&lockfilePath, "lockfile", "", "path to the lockfile to read base image digests from")
	flag.StringVar(&registryOut, "registry-out", "language_registry.yml", "output file for the generated language registry")
	flag.BoolVar(&force, "force", false, "force overwrite existing Dockerfiles")
	flag.Parse()

	tmpl := template.Must(template.New("dockerfile").Parse(dockerfileTemplate))

	var lockfileData imagespec.Lockfile

	if lockfilePath == "" {
		log.Printf("no lockfile specified, base image digests will not be included in generated Dockerfiles")
	} else if _, err := os.Stat(lockfilePath); os.IsNotExist(err) {
		log.Printf("lockfile %s does not exist, base image digests will not be included in generated Dockerfiles", lockfilePath)
	} else {
		lf, err := imagespec.LoadLockfile(lockfilePath)
		if err != nil {
			log.Fatalf("failed to load lockfile: %v", err)
		}
		lockfileData = lf
	}

	entries, err := imagespec.Load(dir)
	if err != nil {
		log.Fatalf("failed to load image specs: %v", err)
	}

	languageRegistry := registry.Registry{Languages: make(map[string]registry.Language)}

	for _, entry := range entries {
		wrapperContent, err := os.ReadFile(filepath.Join(entry.Dir, imagespec.WrapperFilename))
		if err != nil {
			log.Printf("skipping %s: failed to read wrapper script: %v", entry.Spec.Name, err)
			continue
		}

		if entry.Spec.ExecCmd == "" {
			log.Fatalf("%s: exec_cmd is required in the image spec", entry.Spec.Name)
		}

		languageRegistry.Languages[entry.Spec.Name] = registry.Language{
			Name:           entry.Spec.Name,
			Type:           entry.Spec.Type,
			Filename:       entry.Spec.Filename,
			DefaultVersion: entry.Spec.DefaultVersion,
			Env:            entry.Spec.Env,
			Limits:         entry.Spec.Limits,
			CompileLimits:  entry.Spec.CompileLimits,
			Versions:       make(map[string]registry.Version),
			Artifact: registry.Artifact{
				Name:             entry.Spec.Filename,
				ExecutionCommand: entry.Spec.ExecCmd,
			},
		}

		for versionName, version := range entry.Spec.Versions {
			var digest string
			if lockfileData != nil {
				digest = lockfileData[entry.Spec.Name][versionName]
			}
			if lockfileData != nil && digest == "" {
				log.Fatalf("%s %s: no digest found in lockfile", entry.Spec.Name, versionName)
			}
			if digest != "" {
				baseTag, _, _ := strings.Cut(version.BaseImage, "@")
				version.BaseImage = baseTag + "@" + digest
			}
			createDockerfile(entry.Dir, entry.Spec.Name, versionName, force, version, entry.Spec, tmpl, wrapperContent)
			languageRegistry.Languages[entry.Spec.Name].Versions[versionName] = registry.Version{
				Name:     versionName,
				Image:    versionEntryImageNamePrefix + entry.Spec.Name + ":" + versionName,
				RunCmd:   version.RunCmd,
				BuildCmd: version.BuildCmd,
			}
		}
	}

	registryBytes, err := yaml.Marshal(languageRegistry)
	if err != nil {
		log.Fatalf("failed to marshal language registry to YAML: %v", err)
	}

	if err := os.WriteFile(registryOut, registryBytes, 0o644); err != nil {
		log.Fatalf("failed to write language registry to %s: %v", registryOut, err)
	}

	log.Printf("language registry generated successfully at %s", registryOut)
}

func createDockerfile(langDir string, langName string, versionName string, force bool, version imagespec.Version, spec imagespec.ImageSpec, tmpl *template.Template, wrapperContent []byte) {
	outDir := filepath.Join(langDir, versionName)
	outFile := filepath.Join(outDir, "Dockerfile")
	if _, err := os.Stat(outFile); err == nil && !force {
		log.Printf("skipping %s: Dockerfile already exists (use -force to overwrite)", outFile)
		return
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Printf("failed to create directory %s: %v", outDir, err)
		return
	}

	data := dockerfileData{
		BaseImage: version.BaseImage,
		Type:      spec.Type,
	}
	if spec.Type == "compiled" {
		data.BuildCmd = strings.Join(version.BuildCmd, " ")
	}
	jsonBytes, err := json.Marshal(version.RunCmd)
	if err != nil {
		log.Printf("failed to marshal run command for %s %s: %v", langName, versionName, err)
		return
	}
	data.RunCmdJSON = string(jsonBytes)

	out, err := os.Create(outFile)
	if err != nil {
		log.Printf("failed to create %s: %v", outFile, err)
		return
	}
	defer out.Close()

	if err := tmpl.Execute(out, data); err != nil {
		log.Printf("failed to execute template for %s %s: %v", langName, versionName, err)
		return
	}

	dstWrapperFile := filepath.Join(outDir, imagespec.WrapperFilename)
	if err := os.WriteFile(dstWrapperFile, wrapperContent, 0o755); err != nil {
		log.Printf("failed to write wrapper script to %s: %v", dstWrapperFile, err)
		return
	}

	log.Printf("generated Dockerfile for %s %s at %s", langName, versionName, outFile)
}
