package imagespec_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/tpsawant027/runboxd/internal/imagespec"
)

func TestLoadImageSpec(t *testing.T) {
	cases := []struct {
		name               string
		dir                string
		wantErr            bool
		wantErrContains    string
		wantErrNotContains string
		wantEntries        int
	}{
		{
			name:        "valid",
			dir:         "testdata/valid",
			wantErr:     false,
			wantEntries: 2,
		},
		{
			name:            "compiled missing build cmd",
			dir:             "testdata/compiled_missing_build_cmd",
			wantErr:         true,
			wantErrContains: "build_cmd is required",
		},
		{
			name:            "default version not in versions",
			dir:             "testdata/default_version_not_in_versions",
			wantErr:         true,
			wantErrContains: "default_version",
		},
		{
			name:            "invalid type",
			dir:             "testdata/invalid_type",
			wantErr:         true,
			wantErrContains: "type must be either 'interpreted' or 'compiled'",
		},
		{
			name:            "missing base image",
			dir:             "testdata/missing_base_image",
			wantErr:         true,
			wantErrContains: "base_image is required",
		},
		{
			name:            "missing default version",
			dir:             "testdata/missing_default_version",
			wantErr:         true,
			wantErrContains: "default_version is required",
		},
		{
			name:            "missing filename",
			dir:             "testdata/missing_filename",
			wantErr:         true,
			wantErrContains: "filename is required",
		},
		{
			name:            "missing name",
			dir:             "testdata/missing_name",
			wantErr:         true,
			wantErrContains: "name is required",
		},
		{
			name:            "missing run cmd",
			dir:             "testdata/missing_run_cmd",
			wantErr:         true,
			wantErrContains: "run_cmd is required",
		},
		{
			name:            "missing type",
			dir:             "testdata/missing_type",
			wantErr:         true,
			wantErrContains: "type is required",
		},
		{
			name:            "no versions",
			dir:             "testdata/no_versions",
			wantErr:         true,
			wantErrContains: "at least one version is required",
		},
		{
			name:               "malformed yaml",
			dir:                "testdata/malformed_yaml",
			wantErr:            true,
			wantErrContains:    "parse",
			wantErrNotContains: "validate",
		},
		{
			name:    "unknown top key",
			dir:     "testdata/unknown_top_key",
			wantErr: true,
		},
		{
			name:    "unknown nested key",
			dir:     "testdata/unknown_nested_key",
			wantErr: true,
		},
		{
			name:    "unknown version key",
			dir:     "testdata/unknown_version_key",
			wantErr: true,
		},
		{
			name:    "non-existent directory",
			dir:     "testdata/non_existent_directory",
			wantErr: true,
		},
		{
			name:            "spec is directory",
			dir:             "testdata/spec_is_dir",
			wantErr:         true,
			wantErrContains: "image.yml: is a directory",
		},
		{
			name:        "top-level non-dir entry skipped",
			dir:         "testdata/nondir_skipped",
			wantErr:     false,
			wantEntries: 1,
		},
		{
			name:        "subdir without image.yml skipped",
			dir:         "testdata/missing_spec_skipped",
			wantErr:     false,
			wantEntries: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := imagespec.Load(tc.dir)
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("expected error but got none")
			case !tc.wantErr && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr && tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains):
				t.Fatalf("expected error to contain %q but got: %v", tc.wantErrContains, err)
			case tc.wantErr && tc.wantErrNotContains != "" && strings.Contains(err.Error(), tc.wantErrNotContains):
				t.Fatalf("expected error NOT to contain %q but got: %v", tc.wantErrNotContains, err)
			case !tc.wantErr && len(entries) != tc.wantEntries:
				t.Fatalf("expected %d entries but got %d", tc.wantEntries, len(entries))
			}
		})
	}
}

func TestLoadDecodesFields(t *testing.T) {
	entries, err := imagespec.Load("testdata/valid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := make(map[string]imagespec.ImageSpec, len(entries))
	for _, e := range entries {
		byName[e.Spec.Name] = e.Spec
	}

	cc, ok := byName["cc"]
	if !ok {
		t.Fatal("missing cc entry")
	}
	if cc.Type != "compiled" || cc.Filename != "main.c" {
		t.Errorf("cc scalars: got type=%q filename=%q", cc.Type, cc.Filename)
	}
	if cc.CompileLimits.MemoryMiB != 512 || cc.CompileLimits.TimeoutSeconds != 10 {
		t.Errorf("cc compile_limits: got %+v", cc.CompileLimits)
	}
	ccV := cc.Versions["13"]
	if ccV.BaseImage != "gcc:13" {
		t.Errorf("cc base_image: got %q", ccV.BaseImage)
	}
	if !slices.Equal(ccV.BuildCmd, []string{"gcc", "-O2", "-o", "/build/main", "/sandbox/main.c"}) {
		t.Errorf("cc build_cmd: got %q", ccV.BuildCmd)
	}
	if !slices.Equal(ccV.RunCmd, []string{"/build/main"}) {
		t.Errorf("cc run_cmd: got %q", ccV.RunCmd)
	}

	py, ok := byName["python"]
	if !ok {
		t.Fatal("missing python entry")
	}
	if py.Limits.MaxMemoryMiB != 256 || py.Limits.MaxPids != 64 {
		t.Errorf("python limits: got %+v", py.Limits)
	}
	pyV := py.Versions["3.12"]
	if !slices.Equal(pyV.RunCmd, []string{"python3", "/sandbox/main.py"}) {
		t.Errorf("python run_cmd: got %q", pyV.RunCmd)
	}
	if len(pyV.BuildCmd) != 0 {
		t.Errorf("python build_cmd should be empty, got %q", pyV.BuildCmd)
	}
}

func TestLoadLockfile(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		lf, err := imagespec.LoadLockfile("testdata/lockfile_valid.yml")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := imagespec.Lockfile{
			"python": {"3.12": "sha256:aaaa1111"},
			"cc":     {"13": "sha256:bbbb2222", "14": "sha256:cccc3333"},
		}
		if !maps.EqualFunc(lf, want, maps.Equal) {
			t.Fatalf("decoded %#v, want %#v", lf, want)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		_, err := imagespec.LoadLockfile("testdata/lockfile_malformed.yml")
		if err == nil || !strings.Contains(err.Error(), "parse lockfile") {
			t.Fatalf("want parse error, got: %v", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := imagespec.LoadLockfile("testdata/does_not_exist.yml")
		if err == nil || !strings.Contains(err.Error(), "read lockfile") {
			t.Fatalf("want read error, got: %v", err)
		}
	})
}

func TestParseLangFilter(t *testing.T) {
	cases := []struct {
		name            string
		raw             []string
		opts            imagespec.ParseLangFilterOptions
		want            imagespec.LangFilter
		wantErrContains string
	}{
		{
			name: "raw is nil",
			raw:  nil,
			want: imagespec.LangFilter{},
		},
		{
			name: "raw is empty",
			raw:  []string{},
			want: imagespec.LangFilter{},
		},
		{
			name: "single language, no version",
			raw:  []string{"python"},
			want: imagespec.LangFilter{"python": nil},
		},
		{
			name: "single language, single version",
			raw:  []string{"python:3.12"},
			want: imagespec.LangFilter{"python": []string{"3.12"}},
		},
		{
			name: "single language, multiple versions - input order not sorted",
			raw:  []string{"python:3.12,3.11"},
			want: imagespec.LangFilter{"python": []string{"3.11", "3.12"}},
		},
		{
			name: "single language, multiple versions - input order sorted",
			raw:  []string{"python:3.11,3.12"},
			want: imagespec.LangFilter{"python": []string{"3.11", "3.12"}},
		},
		{
			name: "single languge, duplicate versions, separate entries",
			raw:  []string{"python:3.12", "python:3.12"},
			want: imagespec.LangFilter{"python": []string{"3.12"}},
		},
		{
			name: "single language, duplicate versions, same entry",
			raw:  []string{"python:3.12,3.12"},
			want: imagespec.LangFilter{"python": []string{"3.12"}},
		},
		{
			name: "single language, multiple entries merged",
			raw:  []string{"python:3.12", "python:3.11"},
			want: imagespec.LangFilter{"python": []string{"3.11", "3.12"}},
		},
		{
			name: "single language, later bare entry overrides earlier versioned entry",
			raw:  []string{"python:3.12", "python"},
			want: imagespec.LangFilter{"python": nil},
		},
		{
			name: "single language, earlier bare entry overrides later versioned entry",
			raw:  []string{"python", "python:3.12"},
			want: imagespec.LangFilter{"python": nil},
		},
		{
			name: "single language, empty version string same as bare entry",
			raw:  []string{"python:"},
			want: imagespec.LangFilter{"python": nil},
		},
		{
			name: "single language, stray commas ignored",
			raw:  []string{"python:3.12,,3.11"},
			want: imagespec.LangFilter{"python": []string{"3.11", "3.12"}},
		},
		{
			name: "single language, only stray commas treated as bare entry",
			raw:  []string{"python:,,"},
			want: imagespec.LangFilter{"python": nil},
		},
		{
			name: "multiple languages",
			raw:  []string{"python:3.12", "go:1.26", "python:3.11", "go:1.25"},
			want: imagespec.LangFilter{"python": []string{"3.11", "3.12"}, "go": []string{"1.25", "1.26"}},
		},
		{
			name:            "empty language name",
			raw:             []string{":3.12"},
			wantErrContains: "language name is empty",
		},
		{
			name:            "filter exceeds max length",
			raw:             []string{"python:3.12" + strings.Repeat("x", 100)},
			wantErrContains: "exceeds maximum length",
		},
		{
			name: "filter at max length",
			raw:  []string{"python:3.12" + strings.Repeat("x", 89)},
			want: imagespec.LangFilter{"python": []string{"3.12" + strings.Repeat("x", 89)}},
		},
		{
			name: "ignore versions, single language with version",
			raw:  []string{"python:3.12"},
			opts: imagespec.ParseLangFilterOptions{IgnoreVersions: true},
			want: imagespec.LangFilter{"python": nil},
		},
		{
			name: "ignore versions, duplicate entries for same language collapse",
			raw:  []string{"python:3.12", "python:3.11"},
			opts: imagespec.ParseLangFilterOptions{IgnoreVersions: true},
			want: imagespec.LangFilter{"python": nil},
		},
		{
			name: "ignore versions, multiple languages",
			raw:  []string{"python:3.12", "go:1.26"},
			opts: imagespec.ParseLangFilterOptions{IgnoreVersions: true},
			want: imagespec.LangFilter{"python": nil, "go": nil},
		},
		{
			name:            "ignore versions, empty language name still errors",
			raw:             []string{":3.12"},
			opts:            imagespec.ParseLangFilterOptions{IgnoreVersions: true},
			wantErrContains: "language name is empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := imagespec.ParseLangFilter(tc.raw, tc.opts)
			if (tc.wantErrContains == "" && err != nil) || (tc.wantErrContains != "" && err == nil) {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErrContains != "" {
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("expected error to contain %q but got: %v", tc.wantErrContains, err)
				}
			}
			if !maps.EqualFunc(got, tc.want, slices.Equal) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFilterVersions(t *testing.T) {
	dummyVersions := map[string]int{
		"1.0": 1,
		"1.1": 2,
		"1.2": 3,
	}
	cases := []struct {
		name        string
		versions    map[string]int
		requested   []string
		wantKept    map[string]int
		wantMissing []string
	}{
		{
			name:        "all requested versions present",
			versions:    dummyVersions,
			requested:   []string{"1.0", "1.1"},
			wantKept:    map[string]int{"1.0": 1, "1.1": 2},
			wantMissing: nil,
		},
		{
			name:        "some requested versions missing",
			versions:    dummyVersions,
			requested:   []string{"1.0", "1.2", "1.3"},
			wantKept:    map[string]int{"1.0": 1, "1.2": 3},
			wantMissing: []string{"1.3"},
		},
		{
			name:        "requested versions empty",
			versions:    dummyVersions,
			requested:   []string{},
			wantKept:    make(map[string]int, 0),
			wantMissing: nil,
		},
		{
			name:        "requested versions with duplicates which are present are deduped",
			versions:    dummyVersions,
			requested:   []string{"1.0", "1.0", "1.1"},
			wantKept:    map[string]int{"1.0": 1, "1.1": 2},
			wantMissing: nil,
		},
		{
			name:        "requested versions with duplicates which are missing are not deduped",
			versions:    dummyVersions,
			requested:   []string{"1.0", "1.3", "1.3"},
			wantKept:    map[string]int{"1.0": 1},
			wantMissing: []string{"1.3", "1.3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotKept, gotMissing := imagespec.FilterVersions(tc.versions, tc.requested)
			if !maps.Equal(gotKept, tc.wantKept) {
				t.Fatalf("got kept %#v, want %#v", gotKept, tc.wantKept)
			}
			if !slices.Equal(gotMissing, tc.wantMissing) {
				t.Fatalf("got missing %#v, want %#v", gotMissing, tc.wantMissing)
			}
		})
	}
}

func TestFormatMissingVersionsError(t *testing.T) {
	t.Run("all missing versions listed - no truncation", func(t *testing.T) {
		missing := []string{"1.3", "1.4", "1.5"}
		available := []string{"1.0", "1.1", "1.2"}
		err := imagespec.FormatMissingVersionsError("abc", missing, available)
		if err == nil {
			t.Fatal("expected error but got nil")
		}
		errMsg := err.Error()
		if !strings.Contains(errMsg, strings.Join(missing, ", ")) {
			t.Errorf("error message does not contain all missing versions: got %q", errMsg)
		}
		if strings.Contains(errMsg, "...and") {
			t.Errorf("error message should not contain truncation message: got %q", errMsg)
		}
		if !strings.Contains(errMsg, strings.Join(available, ", ")) {
			t.Errorf("error message does not contain available versions: got %q", errMsg)
		}
	})
	t.Run("truncated missing versions", func(t *testing.T) {
		missing := []string{"1.3", "1.4", "1.5", "1.6", "1.7", "1.8", "1.9", "1.10", "1.11", "1.12", "1.13", "1.14", "1.15"}
		available := []string{"1.0", "1.1", "1.2"}
		err := imagespec.FormatMissingVersionsError("abc", missing, available)
		if err == nil {
			t.Fatal("expected error but got nil")
		}
		errMsg := err.Error()
		truncatedNum := len(missing) - 10
		if !strings.Contains(errMsg, fmt.Sprintf("...and %d more", truncatedNum)) {
			t.Errorf("error message does not contain truncation message: got %q", errMsg)
		}
		if !strings.Contains(errMsg, strings.Join(available, ", ")) {
			t.Errorf("error message does not contain available versions: got %q", errMsg)
		}
		if !strings.Contains(errMsg, strings.Join(missing[:10], ", ")) {
			t.Errorf("error message does not contain first 10 missing versions: got %q", errMsg)
		}
		if strings.Contains(errMsg, strings.Join(missing[10:], ", ")) {
			t.Errorf("error message should not contain missing versions beyond the first 10: got %q", errMsg)
		}
	})
}

func TestLoadFiltered(t *testing.T) {
	cases := []struct {
		name            string
		filter          imagespec.LangFilter
		wantErrContains []string
		wantEntries     map[string][]string
	}{
		{
			name:        "filter with one entry, no versions specified",
			filter:      imagespec.LangFilter{"python": nil},
			wantEntries: map[string][]string{"python": {"3.10", "3.11", "3.12"}},
		},
		{
			name:        "filter with one entry, one version specified",
			filter:      imagespec.LangFilter{"python": []string{"3.11"}},
			wantEntries: map[string][]string{"python": {"3.11"}},
		},
		{
			name:        "filter with one entry, multiple versions specified",
			filter:      imagespec.LangFilter{"python": []string{"3.11", "3.12"}},
			wantEntries: map[string][]string{"python": {"3.11", "3.12"}},
		},
		{
			name:        "filter with multiple entries, one of which has no versions specified",
			filter:      imagespec.LangFilter{"python": nil, "go": []string{"1.26"}},
			wantEntries: map[string][]string{"python": {"3.10", "3.11", "3.12"}, "go": {"1.26"}},
		},
		{
			name:            "filter with one entry which is not present",
			filter:          imagespec.LangFilter{"py": nil},
			wantErrContains: []string{"language py: spec file not found"},
			wantEntries:     nil,
		},
		{
			name:            "filter with multiple entries, one of which is not present",
			filter:          imagespec.LangFilter{"python": nil, "py": nil},
			wantErrContains: []string{"language py: spec file not found"},
			wantEntries:     nil,
		},
		{
			name:            "filter with one entry which has a unknown version",
			filter:          imagespec.LangFilter{"python": []string{"3.11", "9.9"}},
			wantErrContains: []string{"unknown version(s) for python: 9.9", "available: 3.10, 3.11, 3.12"},
			wantEntries:     nil,
		},
		{
			name:   "nil filter",
			filter: nil,
			wantEntries: map[string][]string{
				"python": {"3.10", "3.11", "3.12"},
				"go":     {"1.25", "1.26"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := imagespec.LoadFiltered("testdata/filter_multi_version", tc.filter)
			if len(tc.wantErrContains) > 0 && err == nil {
				t.Fatalf("expected error but got none")
			}
			if len(tc.wantErrContains) == 0 && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tc.wantErrContains) > 0 {
				for _, substr := range tc.wantErrContains {
					if !strings.Contains(err.Error(), substr) {
						t.Errorf("expected error to contain %q but got: %v", substr, err)
					}
				}
			}
			if len(tc.wantEntries) > 0 {
				gotEntries := make(map[string][]string)
				for _, e := range entries {
					versions := slices.Sorted(maps.Keys(e.Spec.Versions))
					gotEntries[e.Spec.Name] = versions
				}
				if !maps.EqualFunc(gotEntries, tc.wantEntries, slices.Equal) {
					t.Errorf("got entries %#v, want %#v", gotEntries, tc.wantEntries)
				}
			}
		})
	}
}
