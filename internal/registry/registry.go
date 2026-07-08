// Package registry provides functionality to load and manage a registry of programming languages and their versions from a YAML file.
package registry

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/tpsawant027/runboxd/internal/imagespec"
	"go.yaml.in/yaml/v4"
)

type Registry struct {
	Languages map[string]Language `yaml:"languages"`
}

type Language struct {
	Name           string                  `yaml:"name"`
	Type           string                  `yaml:"type"`
	Filename       string                  `yaml:"filename"`
	DefaultVersion string                  `yaml:"default_version"`
	Env            map[string]string       `yaml:"env,omitempty"`
	Limits         imagespec.Limits        `yaml:"limits"`
	CompileLimits  imagespec.CompileLimits `yaml:"compile_limits,omitempty"`
	Versions       map[string]Version      `yaml:"versions"`
	Artifact       Artifact                `yaml:"artifact"`
}

type Version struct {
	Name     string   `yaml:"name"`
	Image    string   `yaml:"image"`
	RunCmd   []string `yaml:"run_cmd"`
	BuildCmd []string `yaml:"build_cmd"`
}

type Artifact struct {
	Name             string `yaml:"name"`
	ExecutionCommand string `yaml:"execution_command"`
}

func (r *Registry) Filter(filter imagespec.LangFilter) error {
	newLanguages := make(map[string]Language, len(filter))
	langsSeen := make(map[string]struct{})
	var errs []error
	for langName, lang := range r.Languages {
		requestedVersions, ok := filter[langName]
		if !ok {
			continue
		}
		langsSeen[langName] = struct{}{}
		if len(requestedVersions) > 0 {
			newVersions, missing := imagespec.FilterVersions(lang.Versions, requestedVersions)
			if len(missing) > 0 {
				available := slices.Sorted(maps.Keys(lang.Versions))
				errs = append(errs, imagespec.FormatMissingVersionsError(langName, missing, available))
				continue
			}
			lang.Versions = newVersions
		}
		newLanguages[langName] = lang
	}
	for langName := range filter {
		if _, ok := langsSeen[langName]; !ok {
			errs = append(errs, fmt.Errorf("unknown language: %s", langName))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	r.Languages = newLanguages
	return nil
}

func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry file %s: %w", path, err)
	}
	var registry Registry
	if err := yaml.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("parse registry file %s: %w", path, err)
	}
	return &registry, nil
}
