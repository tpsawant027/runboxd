// Package imagespec defines the structure of the image specification and provides functions to load the specifications and lockfile.
package imagespec

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

type LangFilter map[string][]string // language -> requested versions; nil/empty = all versions

const maxRawFilterLength = 100

func ParseLangFilter(raw []string) (LangFilter, error) {
	filter := make(LangFilter)
	for _, r := range raw {
		if len(r) > maxRawFilterLength {
			return nil, fmt.Errorf("filter %q... exceeds maximum length of %d", r[:maxRawFilterLength], maxRawFilterLength)
		}
		lang, versionStr, _ := strings.Cut(r, ":")
		if lang == "" {
			return nil, fmt.Errorf("filter %q: language name is empty", r)
		}
		currVersions, ok := filter[lang]
		switch {
		case versionStr == "":
			filter[lang] = nil
		case ok && currVersions == nil:
			// already requesting all versions of lang; a specific version adds nothing
		case ok:
			filter[lang] = append(currVersions, strings.Split(versionStr, ",")...)
		default:
			filter[lang] = strings.Split(versionStr, ",")
		}
	}
	filter = dedupeLangFilter(filter)
	return filter, nil
}

func dedupeLangFilter(filter LangFilter) LangFilter {
	deduped := make(LangFilter)
	for lang, versions := range filter {
		if versions == nil {
			deduped[lang] = nil
			continue
		}
		versionSet := make(map[string]struct{})
		for _, v := range versions {
			if v == "" {
				continue
			}
			versionSet[v] = struct{}{}
		}
		if len(versionSet) == 0 {
			deduped[lang] = nil
			continue
		}
		deduped[lang] = slices.Sorted(maps.Keys(versionSet))
	}
	return deduped
}

func LoadFiltered(dir string, filter LangFilter) ([]Entry, error) {
	var entries []Entry
	var errs []error

	if filter == nil {
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read dir %s: %w", dir, err)
		}
		for _, f := range files {
			if !f.IsDir() {
				continue
			}
			langDir := filepath.Join(dir, f.Name())
			specPath := filepath.Join(langDir, SpecFilename)
			spec, err := loadImageSpec(specPath)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				errs = append(errs, err)
				continue
			}
			entries = append(entries, Entry{Dir: langDir, Spec: spec})
		}
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		return entries, nil
	}

	for lang, versions := range filter {
		langDir := filepath.Join(dir, lang)
		specPath := filepath.Join(langDir, SpecFilename)
		spec, err := loadImageSpec(specPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("language %s: spec file not found", lang))
				continue
			}
			errs = append(errs, err)
			continue
		}
		if len(versions) > 0 {
			newVersions, missing := FilterVersions(spec.Versions, versions)
			if len(missing) > 0 {
				available := slices.Sorted(maps.Keys(spec.Versions))
				errs = append(errs, FormatMissingVersionsError(lang, missing, available))
				continue
			}
			spec.Versions = newVersions
		}
		entries = append(entries, Entry{Dir: langDir, Spec: spec})

	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return entries, nil
}

func Load(dir string) ([]Entry, error) {
	return LoadFiltered(dir, nil)
}

func FilterVersions[V any](versions map[string]V, requested []string) (kept map[string]V, missing []string) {
	kept = make(map[string]V, len(requested))
	for _, v := range requested {
		if ver, ok := versions[v]; ok {
			kept[v] = ver
		} else {
			missing = append(missing, v)
		}
	}
	return kept, missing
}

func FormatMissingVersionsError(lang string, missing, available []string) error {
	maxNamed := 10
	named, suffix := missing, ""
	if len(missing) > maxNamed {
		named = missing[:maxNamed]
		suffix = fmt.Sprintf(" ...and %d more", len(missing)-maxNamed)
	}
	return fmt.Errorf("unknown version(s) for %s: %s%s (available: %s)",
		lang, strings.Join(named, ", "), suffix, strings.Join(available, ", "))
}

func loadImageSpec(path string) (ImageSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ImageSpec{}, fmt.Errorf("read %s: %w", path, err)
	}
	var spec ImageSpec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return ImageSpec{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validateSpec(spec); err != nil {
		return ImageSpec{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return spec, nil
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
