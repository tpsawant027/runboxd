// Package imagespec defines the structure of the image specification and provides functions to load the specifications and lockfile.
package imagespec

import (
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
		if _, err := os.Stat(specPath); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		data, err := os.ReadFile(specPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", specPath, err)
		}
		var spec ImageSpec
		if err := yaml.Unmarshal(data, &spec); err != nil {
			return nil, fmt.Errorf("parse %s: %w", specPath, err)
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
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parse lockfile %s: %w", path, err)
	}
	return lf, nil
}
