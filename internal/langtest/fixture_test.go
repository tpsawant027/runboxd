package langtest_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tpsawant027/runboxd/internal/imagespec"
	"github.com/tpsawant027/runboxd/internal/langtest"
)

func TestLoadFixture(t *testing.T) {
	cases := []struct {
		name            string
		filename        string
		wantErr         bool
		wantErrContains string
	}{
		{
			name:     "valid",
			filename: "testdata/valid.yml",
			wantErr:  false,
		},
		{
			name:     "compile_error with only source",
			filename: "testdata/compile_error_ok.yml",
			wantErr:  false,
		},
		{
			name:     "smoke with empty status",
			filename: "testdata/smoke_empty_status_ok.yml",
			wantErr:  false,
		},
		{
			name:     "unknown top key",
			filename: "testdata/unknown_top_key.yml",
			wantErr:  true,
		},
		{
			name:     "unknown conformance key",
			filename: "testdata/unknown_conformance_key.yml",
			wantErr:  true,
		},
		{
			name:     "unknown smoke key",
			filename: "testdata/unknown_smoke_key.yml",
			wantErr:  true,
		},
		{
			name:     "malformed yaml",
			filename: "testdata/malformed_yaml.yml",
			wantErr:  true,
		},
		{
			name:            "unknown conformance capability",
			filename:        "testdata/unknown_conformance_capability.yml",
			wantErr:         true,
			wantErrContains: "unknown conformance test case key",
		},
		{
			name:            "conformance missing source",
			filename:        "testdata/conformance_missing_source.yml",
			wantErr:         true,
			wantErrContains: "missing required field 'source'",
		},
		{
			name:            "conformance oom zero memory",
			filename:        "testdata/oom_zero_memory.yml",
			wantErr:         true,
			wantErrContains: "memory_bytes must be > 0",
		},
		{
			name:            "conformance oom negative memory",
			filename:        "testdata/oom_negative_memory.yml",
			wantErr:         true,
			wantErrContains: "memory_bytes must be > 0",
		},
		{
			name:            "conformance timeout zero",
			filename:        "testdata/timeout_zero.yml",
			wantErr:         true,
			wantErrContains: "timeout_ms must be > 0",
		},
		{
			name:            "conformance timeout negative",
			filename:        "testdata/timeout_negative.yml",
			wantErr:         true,
			wantErrContains: "timeout_ms must be > 0",
		},
		{
			name:            "conformance fs_escape no stderr",
			filename:        "testdata/fs_escape_no_stderr.yml",
			wantErr:         true,
			wantErrContains: "want_stderr_contains is required",
		},
		{
			name:            "smoke missing name",
			filename:        "testdata/smoke_missing_name.yml",
			wantErr:         true,
			wantErrContains: "missing required field 'name'",
		},
		{
			name:            "smoke missing source",
			filename:        "testdata/smoke_missing_source.yml",
			wantErr:         true,
			wantErrContains: "missing required field 'source'",
		},
		{
			name:            "smoke unknown status",
			filename:        "testdata/smoke_unknown_status.yml",
			wantErr:         true,
			wantErrContains: "unknown want_status",
		},
		{
			name:     "non-existent file",
			filename: "testdata/non_existent_file.yml",
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := langtest.LoadFixture(tc.filename)
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("expected error but got none")
			case !tc.wantErr && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr && tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains):
				t.Fatalf("expected error to contain %q but got: %v", tc.wantErrContains, err)
			}
		})
	}
}

func TestLoadFixtureDecodesFields(t *testing.T) {
	fx, err := langtest.LoadFixture("testdata/valid.yml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fx.Language != "testlang" || fx.Version != "1.0" {
		t.Errorf("got language=%q version=%q", fx.Language, fx.Version)
	}

	oom := fx.ConformanceTests["oom"]
	if oom.Source != "allocate too much" || oom.MemoryBytes != 1024 {
		t.Errorf("oom conformance: got %+v", oom)
	}
	if got := fx.ConformanceTests["compile_error"].Source; got != "does not compile" {
		t.Errorf("compile_error source: got %q", got)
	}

	if len(fx.SmokeTests) != 2 {
		t.Fatalf("got %d smoke tests, want 2", len(fx.SmokeTests))
	}
	if s := fx.SmokeTests[0]; s.Name != "hello" || s.Source != "print hi" || s.WantStdout != "hi\n" {
		t.Errorf("smoke[0]: got %+v", s)
	}
	if s := fx.SmokeTests[1]; s.WantStatus != "runtime_error" || s.WantStderrContains != "boom" {
		t.Errorf("smoke[1]: got %+v", s)
	}
}

func TestLoad(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		matches, _ := filepath.Glob("../../images/*/tests.yml")
		expectedCount := len(matches)
		fixtures, err := langtest.Load("../../images/*/tests.yml")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(fixtures) == 0 {
			t.Fatalf("expected at least one fixture but got none")
		}
		if len(fixtures) != expectedCount {
			t.Fatalf("expected %d fixtures but got %d", expectedCount, len(fixtures))
		}
	})

	t.Run("invalid glob", func(t *testing.T) {
		_, err := langtest.Load("testdata/[.yml")
		if err == nil || !strings.Contains(err.Error(), "failed to glob fixtures") {
			t.Fatalf("expected glob error but got: %v", err)
		}
	})

	t.Run("multiple errors", func(t *testing.T) {
		_, err := langtest.Load("testdata/multi_err/*.yml")
		wantErrContains := []string{"missing required field 'name'", "memory_bytes must be > 0"}
		if err == nil {
			t.Fatalf("expected error but got none")
		}
		for _, substr := range wantErrContains {
			if !strings.Contains(err.Error(), substr) {
				t.Errorf("expected error to contain %q but got: %v", substr, err)
			}
		}
	})
}

func TestLoadFiltered(t *testing.T) {
	cases := []struct {
		name             string
		dir              string
		filter           imagespec.LangFilter
		wantErrContains  []string
		wantFixtureLangs []string // sorted list of expected languages in the loaded fixtures
	}{
		{
			name:             "nil filter",
			dir:              "testdata/filter/valid",
			filter:           nil,
			wantFixtureLangs: []string{"go", "python"},
		},
		{
			name:             "filter with one language",
			dir:              "testdata/filter/valid/",
			filter:           imagespec.LangFilter{"python": nil},
			wantFixtureLangs: []string{"python"},
		},
		{
			name:             "filter with multiple languages",
			dir:              "testdata/filter/valid/",
			filter:           imagespec.LangFilter{"go": nil, "python": nil},
			wantFixtureLangs: []string{"go", "python"},
		},
		{
			name:            "errors for invalid fixtures",
			dir:             "testdata/filter/invalid/",
			filter:          imagespec.LangFilter{"go": nil, "python": nil},
			wantErrContains: []string{"missing required field 'name'", "memory_bytes must be > 0"},
		},
		{
			name:   "filter with unknown language",
			dir:    "testdata/filter/valid/",
			filter: imagespec.LangFilter{"py": nil},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixtures, err := langtest.LoadFiltered(tc.dir, tc.filter)
			if len(tc.wantErrContains) > 0 && err == nil {
				t.Fatalf("expected error but got none")
			}
			if len(tc.wantErrContains) == 0 && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tc.wantErrContains) > 0 {
				errStr := err.Error()
				for _, substr := range tc.wantErrContains {
					if !strings.Contains(errStr, substr) {
						t.Errorf("expected error to contain %q but got: %v", substr, err)
					}
				}
			}
			if len(tc.wantFixtureLangs) != len(fixtures) {
				t.Fatalf("expected %d fixtures but got %d", len(tc.wantFixtureLangs), len(fixtures))
			}
			if len(tc.wantFixtureLangs) > 0 {
				gotLangs := make([]string, len(fixtures))
				for i, f := range fixtures {
					gotLangs[i] = f.Language
				}
				slices.Sort(gotLangs)
				if !slices.Equal(gotLangs, tc.wantFixtureLangs) {
					t.Errorf("expected fixture languages %v but got %v", tc.wantFixtureLangs, gotLangs)
				}
			}
		})
	}
}
