package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"github.com/tpsawant027/runboxd/internal/imagespec"
	"github.com/tpsawant027/runboxd/internal/registry"
	"go.yaml.in/yaml/v4"
)

var genImagesCmd = &cobra.Command{
	Use:   "gen-images",
	Short: "Generate Dockerfiles for all images",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGenImages(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(genImagesCmd)

	genImagesCmd.Flags().String("lockfile", "", "path to the lockfile to read base image digests from")
}

type dockerfileData struct {
	BaseImage  string
	BuildCmd   string
	RunCmdJSON string
	Type       string
	Setup      []string
}

const versionEntryImageNamePrefix = "runboxd-"

var dockerfileTemplate = template.Must(template.New("dockerfile").Parse(`FROM {{.BaseImage}}
{{- if eq .Type "compiled"}}
ENV BUILD_CMD={{.BuildCmd | printf "%q"}}
{{- end}}
RUN mkdir -p /input /sandbox
COPY wrapper.sh /wrapper.sh
RUN chmod +x /wrapper.sh
{{- range .Setup}}
RUN {{.}}
{{- end}}
ENTRYPOINT ["/wrapper.sh"]
CMD {{.RunCmdJSON}}
`))

func runGenImages(cmd *cobra.Command, _ []string) error {
	imageDir, err := cmd.Flags().GetString("image-dir")
	if err != nil {
		return fmt.Errorf("failed to get flag: %w", err)
	}
	lockfilePath, err := cmd.Flags().GetString("lockfile")
	if err != nil {
		return fmt.Errorf("failed to get flag: %w", err)
	}
	registryOut, err := cmd.Flags().GetString("registry")
	if err != nil {
		return fmt.Errorf("failed to get flag: %w", err)
	}

	var lockfileData imagespec.Lockfile

	if lockfilePath == "" {
		log.Printf("no lockfile specified, base image digests will not be included in generated Dockerfiles")
	} else if _, err := os.Stat(lockfilePath); errors.Is(err, fs.ErrNotExist) {
		log.Printf("lockfile %s does not exist, base image digests will not be included in generated Dockerfiles", lockfilePath)
	} else {
		lf, err := imagespec.LoadLockfile(lockfilePath)
		if err != nil {
			return fmt.Errorf("failed to load lockfile: %w", err)
		}
		lockfileData = lf
	}

	entries, err := imagespec.Load(imageDir)
	if err != nil {
		return fmt.Errorf("failed to load image specs: %w", err)
	}

	languageRegistry := registry.Registry{Languages: make(map[string]registry.Language)}

	for _, entry := range entries {
		wrapperContent, err := os.ReadFile(filepath.Join(entry.Dir, imagespec.WrapperFilename))
		if err != nil {
			log.Printf("skipping %s: failed to read wrapper script: %v", entry.Spec.Name, err)
			continue
		}

		if entry.Spec.ExecCmd == "" {
			return fmt.Errorf("%s: exec_cmd is required in the image spec", entry.Spec.Name)
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
				return fmt.Errorf("%s %s: no digest found in lockfile", entry.Spec.Name, versionName)
			}
			if digest != "" {
				baseTag, _, _ := strings.Cut(version.BaseImage, "@")
				version.BaseImage = baseTag + "@" + digest
			}
			if err := createDockerfile(entry.Dir, entry.Spec.Name, versionName, version, entry.Spec, dockerfileTemplate, wrapperContent); err != nil {
				return fmt.Errorf("failed to create Dockerfile for %s %s: %w", entry.Spec.Name, versionName, err)
			}
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
		return fmt.Errorf("failed to marshal language registry to YAML: %w", err)
	}

	if err := os.WriteFile(registryOut, registryBytes, 0o644); err != nil {
		return fmt.Errorf("failed to write language registry to %s: %w", registryOut, err)
	}

	log.Printf("language registry generated successfully at %s", registryOut)
	return nil
}

func createDockerfile(langDir string, langName string, versionName string, version imagespec.Version, spec imagespec.ImageSpec, tmpl *template.Template, wrapperContent []byte) error {
	outDir := filepath.Join(langDir, versionName)
	outFile := filepath.Join(outDir, "Dockerfile")

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data := dockerfileData{
		BaseImage: version.BaseImage,
		Type:      spec.Type,
		Setup:     spec.Setup,
	}
	if spec.Type == "compiled" {
		data.BuildCmd = shellJoin(version.BuildCmd)
	}
	jsonBytes, err := json.Marshal(version.RunCmd)
	if err != nil {
		return fmt.Errorf("failed to marshal run command: %w", err)
	}
	data.RunCmdJSON = string(jsonBytes)

	var outBuf bytes.Buffer

	if err := tmpl.Execute(&outBuf, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	changed, err := writeIfChanged(outFile, outBuf.Bytes(), 0o644)
	if err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	dstWrapperFile := filepath.Join(outDir, imagespec.WrapperFilename)
	if _, err := writeIfChanged(dstWrapperFile, wrapperContent, 0o755); err != nil {
		return fmt.Errorf("failed to write wrapper script: %w", err)
	}

	if changed {
		log.Printf("generated Dockerfile for %s %s at %s", langName, versionName, outFile)
	} else {
		log.Printf("Dockerfile for %s %s is up to date, skipping", langName, versionName)
	}

	return nil
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

func writeIfChanged(path string, content []byte, perm os.FileMode) (bool, error) {
	existingContent, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("failed to read existing file: %w", err)
	}
	if bytes.Equal(existingContent, content) {
		return false, nil
	}
	if err := os.WriteFile(path, content, perm); err != nil {
		return false, fmt.Errorf("failed to write file: %w", err)
	}
	return true, nil
}
