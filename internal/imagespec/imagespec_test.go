package imagespec_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/tpsawant027/runboxd/internal/imagespec"
)

func TestLoadImageSpec(t *testing.T) {
	cases := []struct {
		name            string
		dir             string
		wantErr         bool
		wantErrContains string
		wantEntries     int
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
			name:            "missing exec cmd",
			dir:             "testdata/missing_exec_cmd",
			wantErr:         true,
			wantErrContains: "exec_cmd is required",
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
			name:    "malformed yaml",
			dir:     "testdata/malformed_yaml",
			wantErr: true,
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
	if cc.Type != "compiled" || cc.Filename != "main.c" || cc.ExecCmd != "gcc" {
		t.Errorf("cc scalars: got type=%q filename=%q exec_cmd=%q", cc.Type, cc.Filename, cc.ExecCmd)
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
