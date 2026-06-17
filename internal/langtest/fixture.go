package langtest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tpsawant027/runboxd/internal/sandbox"
	"go.yaml.in/yaml/v4"
)

type Fixture struct {
	Language         string                         `yaml:"language"`
	Version          string                         `yaml:"version"` // empty = default version
	ConformanceTests map[string]ConformanceTestCase `yaml:"conformance"`
	SmokeTests       []SmokeTestCase                `yaml:"smoke"`
}

type ConformanceTestCase struct {
	Source             string `yaml:"source"`
	MemoryBytes        int64  `yaml:"memory_bytes"`         // required iff key == "oom"
	TimeoutMS          int    `yaml:"timeout_ms"`           // required iff key == "timeout"
	WantStderrContains string `yaml:"want_stderr_contains"` // optional
}

type SmokeTestCase struct {
	Name               string        `yaml:"name"`
	Source             string        `yaml:"source"`
	Stdin              string        `yaml:"stdin,omitempty"`
	Files              []FixtureFile `yaml:"files,omitempty"`
	TimeoutMS          int           `yaml:"timeout_ms,omitempty"`
	MemoryBytes        int64         `yaml:"memory_bytes,omitempty"`
	WantStatus         string        `yaml:"want_status"`              // default "ok"
	WantExitCode       *int          `yaml:"want_exit_code,omitempty"` // nil = don't check
	WantStdout         string        `yaml:"want_stdout,omitempty"`
	WantStdoutContains string        `yaml:"want_stdout_contains,omitempty"`
	WantStderrContains string        `yaml:"want_stderr_contains,omitempty"`
}

type FixtureFile struct {
	Name    string `yaml:"name"`
	Content string `yaml:"content"`
}

type CaseResult struct {
	Name   string
	Passed bool
	Detail string
}

var CapabilityStatus = map[string]sandbox.Status{
	"oom":           sandbox.StatusOOM,
	"timeout":       sandbox.StatusTimeout,
	"fs_escape":     sandbox.StatusRuntimeError,
	"compile_error": sandbox.StatusCompileError,
}

var SmokeStatuses = map[string]sandbox.Status{
	"ok":            sandbox.StatusOK,
	"runtime_error": sandbox.StatusRuntimeError,
	"timeout":       sandbox.StatusTimeout,
	"oom":           sandbox.StatusOOM,
	"compile_error": sandbox.StatusCompileError,
}

func Load(glob string) ([]Fixture, error) {
	matches, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("failed to glob fixtures: %w", err)
	}
	var fixtures []Fixture
	for _, match := range matches {
		f, err := LoadFixture(match)
		if err != nil {
			return nil, fmt.Errorf("failed to load fixture %s: %w", match, err)
		}
		fixtures = append(fixtures, f)
	}
	return fixtures, nil
}

func LoadFixture(path string) (Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, fmt.Errorf("read fixture file: %w", err)
	}
	var f Fixture
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("parse fixture YAML: %w", err)
	}
	if err := validateConformanceTestCases(f.ConformanceTests); err != nil {
		return f, fmt.Errorf("validate conformance test cases: %w", err)
	}
	if err := validateSmokeTestCases(f.SmokeTests); err != nil {
		return f, fmt.Errorf("validate smoke test cases: %w", err)
	}
	return f, nil
}

func validateConformanceTestCases(cases map[string]ConformanceTestCase) error {
	for key, tc := range cases {
		if _, ok := CapabilityStatus[key]; !ok {
			return fmt.Errorf("unknown conformance test case key %q", key)
		}
		if tc.Source == "" {
			return fmt.Errorf("conformance test case %q: missing required field 'source'", key)
		}
		switch key {
		case "oom":
			if tc.MemoryBytes <= 0 {
				return fmt.Errorf("conformance test case %q: memory_bytes must be > 0", key)
			}
		case "timeout":
			if tc.TimeoutMS <= 0 {
				return fmt.Errorf("conformance test case %q: timeout_ms must be > 0", key)
			}
		case "fs_escape":
			if tc.WantStderrContains == "" {
				return fmt.Errorf("conformance test case %q: want_stderr_contains is required", key)
			}
		}
	}
	return nil
}

func validateSmokeTestCases(cases []SmokeTestCase) error {
	for i, tc := range cases {
		if tc.Name == "" {
			return fmt.Errorf("smoke test case %d: missing required field 'name'", i)
		}
		if tc.Source == "" {
			return fmt.Errorf("smoke test case %q: missing required field 'source'", tc.Name)
		}
		if tc.WantStatus != "" {
			if _, ok := SmokeStatuses[tc.WantStatus]; !ok {
				return fmt.Errorf("smoke test case %q: unknown want_status %q", tc.Name, tc.WantStatus)
			}
		}
	}
	return nil
}
