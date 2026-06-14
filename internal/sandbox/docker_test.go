package sandbox

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tpsawant027/runboxd/internal/imagespec"
)

func TestStatusForExit(t *testing.T) {
	tests := []struct {
		name string
		code int64
		want Status
	}{
		{"zero is ok", 0, StatusOK},
		{"one is runtime error", 1, StatusRuntimeError},
		{"nonzero is runtime error", 137, StatusRuntimeError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusForExit(tt.code); got != tt.want {
				t.Errorf("statusForExit(%d) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestLimitWriter(t *testing.T) {
	cases := []struct {
		name   string
		limit  int64
		writes []string
		want   string
	}{
		{
			name:   "under limit",
			limit:  10,
			writes: []string{"hello"},
			want:   "hello",
		},
		{
			name:   "exact limit",
			limit:  5,
			writes: []string{"hello"},
			want:   "hello",
		},
		{
			name:   "over limit single write",
			limit:  3,
			writes: []string{"hello"},
			want:   "hel",
		},
		{
			name:   "over limit across writes",
			limit:  5,
			writes: []string{"hel", "lo world"},
			want:   "hello",
		},
		{
			name:   "after limit exhausted",
			limit:  3,
			writes: []string{"hel", "lo"},
			want:   "hel",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lw := &limitWriter{n: tc.limit}
			for _, w := range tc.writes {
				n, err := lw.Write([]byte(w))
				if err != nil {
					t.Fatalf("Write(%q): unexpected error: %v", w, err)
				}
				if n != len(w) {
					t.Errorf("Write(%q) = %d, want %d", w, n, len(w))
				}
			}
			if got := lw.buf.String(); got != tc.want {
				t.Errorf("buf = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("returns full len to avoid stdcopy short-write error", func(t *testing.T) {
		lw := &limitWriter{n: 0}
		n, err := lw.Write([]byte("ignored"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != len("ignored") {
			t.Errorf("Write returned %d, want %d", n, len("ignored"))
		}
		if lw.buf.Len() != 0 {
			t.Errorf("buf should be empty, got %q", lw.buf.String())
		}
	})
}

func TestLookupSpecUnsupportedLanguage(t *testing.T) {
	sb := &DockerSandbox{specs: map[string]langEntry{}}
	_, err := sb.lookupSpec("unsupported-language", "")
	if err == nil {
		t.Fatal("expected error for unsupported language, got nil")
	}
	if !errors.Is(err, ErrUnsupportedLanguage) {
		t.Fatalf("expected ErrUnsupportedLanguage, got %v", err)
	}
}

func TestIsOrphan(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	const maxAge = time.Minute
	tests := []struct {
		name    string
		created int64
		maxAge  time.Duration
		want    bool
	}{
		{"young is not orphan", now.Unix(), maxAge, false},
		{"older than maxAge is orphan", now.Add(-2 * time.Minute).Unix(), maxAge, true},
		{"exactly at cutoff is orphan", now.Add(-maxAge).Unix(), maxAge, true},
		{"one second inside cutoff is not orphan", now.Add(-maxAge).Unix() + 1, maxAge, false},
		{"maxAge zero treats everything as orphan", now.Unix(), 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOrphan(tc.created, now, tc.maxAge); got != tc.want {
				t.Errorf("isOrphan(%d, now, %v) = %v, want %v", tc.created, tc.maxAge, got, tc.want)
			}
		})
	}
}

func fillUnsetLangLimits(input LangLimits) LangLimits {
	input.MinTimeout = valueWithDefault(input.MinTimeout, MinTimeout)
	input.MaxTimeout = valueWithDefault(input.MaxTimeout, MaxTimeout)
	input.MinMemoryBytes = valueWithDefault(input.MinMemoryBytes, MinMemoryBytes)
	input.MaxMemoryBytes = valueWithDefault(input.MaxMemoryBytes, MaxMemoryBytes)
	input.MaxPids = valueWithDefault(input.MaxPids, MaxPids)
	input.MaxCPUs = valueWithDefault(input.MaxCPUs, DefaultMaxCPUs)
	return input
}

func TestResolveLangLimits(t *testing.T) {
	cases := []struct {
		name  string
		input imagespec.Limits
		want  LangLimits
	}{
		{
			// Anchored against the literal consts (not fillUnsetLangLimits) so a
			// wrong default or a bug in valueWithDefault can't hide here.
			name:  "all unset",
			input: imagespec.Limits{},
			want: LangLimits{
				MinTimeout:     MinTimeout,
				MaxTimeout:     MaxTimeout,
				MinMemoryBytes: MinMemoryBytes,
				MaxMemoryBytes: MaxMemoryBytes,
				MaxPids:        MaxPids,
				MaxCPUs:        DefaultMaxCPUs,
			},
		},
		{
			name:  "only max memory set",
			input: imagespec.Limits{MaxMemoryMiB: 128},
			want:  fillUnsetLangLimits(LangLimits{MaxMemoryBytes: 128 * 1024 * 1024}),
		},
		{
			name:  "only min timeout set",
			input: imagespec.Limits{MinTimeoutSeconds: 2},
			want:  fillUnsetLangLimits(LangLimits{MinTimeout: 2 * time.Second}),
		},
		{
			name:  "only max cpus set",
			input: imagespec.Limits{MaxCPUs: 2.0},
			want:  fillUnsetLangLimits(LangLimits{MaxCPUs: 2.0}),
		},
		{
			name:  "low max clamps unset min: memory",
			input: imagespec.Limits{MaxMemoryMiB: 32},
			want:  fillUnsetLangLimits(LangLimits{MinMemoryBytes: 32 * 1024 * 1024, MaxMemoryBytes: 32 * 1024 * 1024}),
		},
		{
			name: "explicit min and max",
			input: imagespec.Limits{
				MinMemoryMiB: 64,
				MaxMemoryMiB: 256,
			},
			want: fillUnsetLangLimits(LangLimits{MinMemoryBytes: 64 * 1024 * 1024, MaxMemoryBytes: 256 * 1024 * 1024}),
		},
		{
			name:  "low max clamps unset min: timeout",
			input: imagespec.Limits{MaxTimeoutSeconds: 1},
			want:  fillUnsetLangLimits(LangLimits{MinTimeout: time.Second, MaxTimeout: time.Second}),
		},
		{
			name:  "only pids set",
			input: imagespec.Limits{MaxPids: 10},
			want:  fillUnsetLangLimits(LangLimits{MaxPids: 10}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveLangLimits(tc.input)
			if got != tc.want {
				t.Errorf("resolveLangLimits(%v) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateLangLimits(t *testing.T) {
	validLimits := fillUnsetLangLimits(LangLimits{})
	cases := []struct {
		name    string
		limits  LangLimits
		wantErr bool
	}{
		{
			name:    "valid limits",
			limits:  validLimits,
			wantErr: false,
		},
		{
			name: "negative min timeout",
			limits: func() LangLimits {
				l := validLimits
				l.MinTimeout = -time.Second
				return l
			}(),
			wantErr: true,
		},
		{
			name: "max timeout less than min",
			limits: func() LangLimits {
				l := validLimits
				l.MinTimeout = 2 * time.Second
				l.MaxTimeout = time.Second
				return l
			}(),
			wantErr: true,
		},
		{
			name: "negative min memory",
			limits: func() LangLimits {
				l := validLimits
				l.MinMemoryBytes = -1024
				return l
			}(),
			wantErr: true,
		},
		{
			name: "max memory less than min",
			limits: func() LangLimits {
				l := validLimits
				l.MinMemoryBytes = 128 * 1024 * 1024
				l.MaxMemoryBytes = 64 * 1024 * 1024
				return l
			}(),
			wantErr: true,
		},
		{
			name: "negative max pids",
			limits: func() LangLimits {
				l := validLimits
				l.MaxPids = -1
				return l
			}(),
			wantErr: true,
		},
		{
			// Pins the `< 1` boundary: zero must fail, not just negatives.
			name: "zero max pids",
			limits: func() LangLimits {
				l := validLimits
				l.MaxPids = 0
				return l
			}(),
			wantErr: true,
		},
		{
			// Operator-trust: consts are defaults, not hard ceilings. Limits
			// above the old global ceiling must validate.
			name: "above old global ceiling is allowed",
			limits: func() LangLimits {
				l := validLimits
				l.MaxMemoryBytes = 512 * 1024 * 1024
				l.MaxPids = 1000
				return l
			}(),
			wantErr: false,
		},
		{
			name: "negative max cpus",
			limits: func() LangLimits {
				l := validLimits
				l.MaxCPUs = -0.1
				return l
			}(),
			wantErr: true,
		},
		{
			// Zero (hand-pinned registry) must fail: it maps to UNLIMITED CPU.
			name: "zero max cpus",
			limits: func() LangLimits {
				l := validLimits
				l.MaxCPUs = 0
				return l
			}(),
			wantErr: true,
		},
		{
			// NaN slips past a bare `<= 0` (all NaN comparisons are false) and
			// converts to a garbage int64 at the backend.
			name: "nan max cpus",
			limits: func() LangLimits {
				l := validLimits
				l.MaxCPUs = math.NaN()
				return l
			}(),
			wantErr: true,
		},
		{
			// +Inf is > 0 so it passes the positivity check; must be rejected
			// separately or it converts to a garbage int64 at the backend.
			name: "positive infinity max cpus",
			limits: func() LangLimits {
				l := validLimits
				l.MaxCPUs = math.Inf(1)
				return l
			}(),
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLangLimits(tc.limits)
			if tc.wantErr && err == nil {
				t.Errorf("validateLangLimits(%v) = nil, want error", tc.limits)
			} else if !tc.wantErr && err != nil {
				t.Errorf("validateLangLimits(%v) = %v, want nil", tc.limits, err)
			}
		})
	}
}

func TestEffectiveTimeout(t *testing.T) {
	langLimits := fillUnsetLangLimits(LangLimits{MinTimeout: 5 * time.Second, MaxTimeout: 10 * time.Second})
	cases := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{
			name:    "zero timeout becomes max",
			timeout: 0,
			want:    langLimits.MaxTimeout,
		},
		{
			name:    "negative timeout becomes max",
			timeout: -time.Second,
			want:    langLimits.MaxTimeout,
		},
		{
			name:    "timeout within limits is unchanged",
			timeout: langLimits.MinTimeout + time.Second,
			want:    langLimits.MinTimeout + time.Second,
		},
		{
			name:    "timeout above max is clamped",
			timeout: langLimits.MaxTimeout + time.Second,
			want:    langLimits.MaxTimeout,
		},
		{
			name:    "timeout below min is clamped",
			timeout: langLimits.MinTimeout - time.Second,
			want:    langLimits.MinTimeout,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveTimeout(tc.timeout, langLimits)
			if got != tc.want {
				t.Errorf("effectiveTimeout(%v, %+v) = %v, want %v", tc.timeout, langLimits, got, tc.want)
			}
		})
	}
}

func TestGetHostConfig(t *testing.T) {
	langLimits := fillUnsetLangLimits(LangLimits{MinMemoryBytes: 64 * 1024 * 1024, MaxMemoryBytes: 256 * 1024 * 1024, MaxPids: 10})
	cases := []struct {
		name        string
		memoryBytes int64
		wantMemory  int64
	}{
		{
			name:        "zero memory becomes max",
			memoryBytes: 0,
			wantMemory:  langLimits.MaxMemoryBytes,
		},
		{
			name:        "within limits is unchanged",
			memoryBytes: 128 * 1024 * 1024,
			wantMemory:  128 * 1024 * 1024,
		},
		{
			name:        "above max is clamped",
			memoryBytes: langLimits.MaxMemoryBytes + 1024,
			wantMemory:  langLimits.MaxMemoryBytes,
		},
		{
			name:        "below min is clamped",
			memoryBytes: langLimits.MinMemoryBytes - 1024,
			wantMemory:  langLimits.MinMemoryBytes,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := RunSpec{MemoryBytes: tc.memoryBytes}
			ds := dockerSpec{limits: langLimits}
			hostConfig := getHostConfig(rs, ds, "tmpdir")
			if hostConfig.Memory != tc.wantMemory {
				t.Errorf("getHostConfig(...).Memory = %d, want %d", hostConfig.Memory, tc.wantMemory)
			}
			if hostConfig.MemorySwap != tc.wantMemory {
				t.Errorf("getHostConfig(...).MemorySwap = %d, want %d", hostConfig.MemorySwap, tc.wantMemory)
			}
			if hostConfig.PidsLimit == nil || *hostConfig.PidsLimit != langLimits.MaxPids {
				t.Errorf("getHostConfig(...).PidsLimit = %v, want %d", hostConfig.PidsLimit, langLimits.MaxPids)
			}
		})
	}
}

func TestLangSpec(t *testing.T) {
	sb := &DockerSandbox{
		specs: map[string]langEntry{
			"python": {
				defaultVersion: "3.14",
				versions:       map[string]versionSpec{"3.14": {image: "python:3.14"}, "3.13": {image: "python:3.13"}, "3.12": {image: "python:3.12"}},
				filename:       "main.py",
				limits:         fillUnsetLangLimits(LangLimits{}),
			},
		},
	}

	cases := []struct {
		name       string
		language   string
		version    string
		wantErr    error
		wantResult LangSpec
	}{
		{
			name:     "valid language and version",
			language: "python",
			version:  "3.14",
			wantErr:  nil,
			wantResult: LangSpec{
				Filename: "main.py",
				Limits:   fillUnsetLangLimits(LangLimits{}),
			},
		},
		{
			name:     "valid language with default version",
			language: "python",
			version:  "",
			wantErr:  nil,
			wantResult: LangSpec{
				Filename: "main.py",
				Limits:   fillUnsetLangLimits(LangLimits{}),
			},
		},
		{
			name:     "unsupported language",
			language: "ruby",
			version:  "3.0",
			wantErr:  ErrUnsupportedLanguage,
		},
		{
			name:     "unsupported version",
			language: "python",
			version:  "3.11",
			wantErr:  ErrUnsupportedVersion,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sb.LangSpec(tc.language, tc.version)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("LangSpec(%q, %q) error = %v, want %v", tc.language, tc.version, err, tc.wantErr)
			}
			if err == nil && got != tc.wantResult {
				t.Errorf("LangSpec(%q, %q) = %+v, want %+v", tc.language, tc.version, got, tc.wantResult)
			}
		})
	}
}

func TestSetupWorkspace(t *testing.T) {
	t.Run("stages code and workspace files", func(t *testing.T) {
		const code = "print('code wins')"
		files := []WorkspaceFile{
			{Path: "helper.py", Content: "def help(): pass"},
			{Path: "data/input.txt", Content: "nested content"},
			{Path: "main.py", Content: "this should be overwritten by code"},
		}

		tmpDir, err := setupWorkspace("test-*", code, "main.py", files)
		if err != nil {
			t.Fatalf("setupWorkspace: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		assertFile := func(rel, want string) {
			t.Helper()
			got, err := os.ReadFile(filepath.Join(tmpDir, inputDir, rel))
			if err != nil {
				t.Fatalf("reading %s: %v", rel, err)
			}
			if string(got) != want {
				t.Errorf("%s = %q, want %q", rel, got, want)
			}
		}

		assertFile("helper.py", "def help(): pass")
		assertFile("data/input.txt", "nested content")
		// Code is written last, so it wins a collision with a workspace file.
		assertFile("main.py", code)
	})

	t.Run("rejects non-local path and cleans up", func(t *testing.T) {
		tmpDir, err := setupWorkspace("test-*", "code", "main.py", []WorkspaceFile{
			{Path: "../escape", Content: "nope"},
		})
		if err == nil {
			t.Fatalf("expected error for non-local path, got nil")
		}
		if tmpDir != "" {
			if _, statErr := os.Stat(tmpDir); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("tmpDir %q should have been removed, stat err = %v", tmpDir, statErr)
			}
		}
	})
}
