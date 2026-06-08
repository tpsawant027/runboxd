package sandbox

import (
	"errors"
	"testing"
	"time"
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOrphan(tt.created, now, tt.maxAge); got != tt.want {
				t.Errorf("isOrphan(%d, now, %v) = %v, want %v", tt.created, tt.maxAge, got, tt.want)
			}
		})
	}
}
