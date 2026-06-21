// Package imagespec defines the structure of the image specification and provides functions to load the specifications and lockfile.
package imagespec

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"
)

const (
	SpecFilename    = "image.yml"
	WrapperFilename = "wrapper.sh"
)

type ImageSpec struct {
	Name           string             `yaml:"name"`
	Type           string             `yaml:"type"`
	Filename       string             `yaml:"filename"`
	DefaultVersion string             `yaml:"default_version"`
	ExecCmd        string             `yaml:"exec_cmd"`
	Env            map[string]string  `yaml:"env,omitempty"`
	Limits         Limits             `yaml:"limits"`
	CompileLimits  CompileLimits      `yaml:"compile_limits,omitempty"`
	Setup          []string           `yaml:"setup,omitempty"`
	Versions       map[string]Version `yaml:"versions"`
}

type Limits struct {
	MinMemoryMiB      int     `yaml:"min_memory_mib,omitempty"`
	MaxMemoryMiB      int     `yaml:"max_memory_mib,omitempty"`
	MinTimeoutSeconds int     `yaml:"min_timeout_seconds,omitempty"`
	MaxTimeoutSeconds int     `yaml:"max_timeout_seconds,omitempty"`
	MaxPids           int     `yaml:"max_pids,omitempty"`
	MaxCPUs           float64 `yaml:"max_cpus,omitempty"`
	WorkspaceSizeMiB  int     `yaml:"workspace_size_mib,omitempty"`
	TmpSizeMiB        int     `yaml:"tmp_size_mib,omitempty"`
}

type CompileLimits struct {
	MemoryMiB        int     `yaml:"memory_mib,omitempty"`
	TimeoutSeconds   int     `yaml:"timeout_seconds,omitempty"`
	MaxPids          int     `yaml:"max_pids,omitempty"`
	MaxCPUs          float64 `yaml:"max_cpus,omitempty"`
	WorkspaceSizeMiB int     `yaml:"workspace_size_mib,omitempty"`
	TmpSizeMiB       int     `yaml:"tmp_size_mib,omitempty"`
}

type Version struct {
	BaseImage string   `yaml:"base_image"`
	BuildCmd  []string `yaml:"build_cmd"`
	RunCmd    []string `yaml:"run_cmd"`
}

type Entry struct {
	Dir  string
	Spec ImageSpec
}

type Lockfile map[string]map[string]string // lang -> version -> digest

func Load(dir string) ([]Entry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	var entries []Entry
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		langDir := filepath.Join(dir, f.Name())
		specPath := filepath.Join(langDir, SpecFilename)
		data, err := os.ReadFile(specPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", specPath, err)
		}
		var spec ImageSpec
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&spec); err != nil {
			return nil, fmt.Errorf("parse %s: %w", specPath, err)
		}
		if err := validateSpec(spec); err != nil {
			return nil, fmt.Errorf("validate %s: %w", specPath, err)
		}
		entries = append(entries, Entry{Dir: langDir, Spec: spec})
	}
	return entries, nil
}

func LoadLockfile(path string) (Lockfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read lockfile %s: %w", path, err)
	}
	var lf Lockfile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&lf); err != nil {
		return nil, fmt.Errorf("parse lockfile %s: %w", path, err)
	}
	return lf, nil
}

func validateSpec(spec ImageSpec) error {
	if spec.Name == "" {
		return errors.New("name is required")
	}
	if spec.Type == "" {
		return errors.New("type is required")
	}
	if spec.Type != "interpreted" && spec.Type != "compiled" {
		return errors.New("type must be either 'interpreted' or 'compiled'")
	}
	if spec.Filename == "" {
		return errors.New("filename is required")
	}
	if spec.DefaultVersion == "" {
		return errors.New("default_version is required")
	}
	if spec.ExecCmd == "" {
		return errors.New("exec_cmd is required")
	}
	if len(spec.Versions) == 0 {
		return errors.New("at least one version is required")
	}
	defaultVersionInVersions := false
	for versionName, version := range spec.Versions {
		if version.BaseImage == "" {
			return fmt.Errorf("base_image is required for version %s", versionName)
		}
		if spec.Type == "compiled" && len(version.BuildCmd) == 0 {
			return fmt.Errorf("build_cmd is required for version %s", versionName)
		}
		if len(version.RunCmd) == 0 {
			return fmt.Errorf("run_cmd is required for version %s", versionName)
		}
		if versionName == spec.DefaultVersion {
			defaultVersionInVersions = true
		}
	}
	if !defaultVersionInVersions {
		return fmt.Errorf("default_version %s is not defined in versions", spec.DefaultVersion)
	}
	return nil
}
