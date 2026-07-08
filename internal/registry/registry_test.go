package registry_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/tpsawant027/runboxd/internal/imagespec"
	"github.com/tpsawant027/runboxd/internal/registry"
)

func newTestRegistry() *registry.Registry {
	return &registry.Registry{Languages: map[string]registry.Language{
		"python": {Name: "python", Versions: map[string]registry.Version{
			"3.10": {Name: "3.10"}, "3.11": {Name: "3.11"}, "3.12": {Name: "3.12"},
		}},
		"go": {Name: "go", Versions: map[string]registry.Version{
			"1.21": {Name: "1.21"}, "1.22": {Name: "1.22"},
		}},
	}}
}

func versionKeys(lang registry.Language) []string {
	return slices.Sorted(maps.Keys(lang.Versions))
}

func langKeys(r *registry.Registry) []string {
	return slices.Sorted(maps.Keys(r.Languages))
}

func TestFilter(t *testing.T) {
	t.Run("bare filter keeps only requested language, all versions", func(t *testing.T) {
		r := newTestRegistry()
		if err := r.Filter(imagespec.LangFilter{"python": nil}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Equal(langKeys(r), []string{"python"}) {
			t.Fatalf("got languages %v, want [python]", langKeys(r))
		}
		if !slices.Equal(versionKeys(r.Languages["python"]), []string{"3.10", "3.11", "3.12"}) {
			t.Fatalf("got python versions %v, want all 3", versionKeys(r.Languages["python"]))
		}
	})

	t.Run("single version narrows Versions map", func(t *testing.T) {
		r := newTestRegistry()
		if err := r.Filter(imagespec.LangFilter{"python": {"3.11"}}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Equal(versionKeys(r.Languages["python"]), []string{"3.11"}) {
			t.Fatalf("got python versions %v, want [3.11]", versionKeys(r.Languages["python"]))
		}
	})

	t.Run("multiple versions narrows Versions map to exactly those", func(t *testing.T) {
		r := newTestRegistry()
		if err := r.Filter(imagespec.LangFilter{"python": {"3.10", "3.12"}}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Equal(versionKeys(r.Languages["python"]), []string{"3.10", "3.12"}) {
			t.Fatalf("got python versions %v, want [3.10 3.12]", versionKeys(r.Languages["python"]))
		}
	})

	t.Run("unknown language errors and leaves registry unchanged", func(t *testing.T) {
		r := newTestRegistry()
		err := r.Filter(imagespec.LangFilter{"py": nil})
		if err == nil || !strings.Contains(err.Error(), "unknown language: py") {
			t.Fatalf("expected error containing %q, got %v", "unknown language: py", err)
		}
		if !slices.Equal(langKeys(r), []string{"go", "python"}) {
			t.Fatalf("registry should be unchanged, got languages %v", langKeys(r))
		}
		if !slices.Equal(versionKeys(r.Languages["python"]), []string{"3.10", "3.11", "3.12"}) {
			t.Fatalf("python versions should be unchanged, got %v", versionKeys(r.Languages["python"]))
		}
		if !slices.Equal(versionKeys(r.Languages["go"]), []string{"1.21", "1.22"}) {
			t.Fatalf("go versions should be unchanged, got %v", versionKeys(r.Languages["go"]))
		}
	})

	t.Run("unknown version errors and leaves registry unchanged", func(t *testing.T) {
		r := newTestRegistry()
		err := r.Filter(imagespec.LangFilter{"python": {"3.11", "9.9"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "unknown version(s) for python") || !strings.Contains(err.Error(), "available: 3.10, 3.11, 3.12") {
			t.Fatalf("unexpected error message: %v", err)
		}
		if !slices.Equal(langKeys(r), []string{"go", "python"}) {
			t.Fatalf("registry should be unchanged, got languages %v", langKeys(r))
		}
		if !slices.Equal(versionKeys(r.Languages["python"]), []string{"3.10", "3.11", "3.12"}) {
			t.Fatalf("python versions should be unchanged, got %v", versionKeys(r.Languages["python"]))
		}
		if !slices.Equal(versionKeys(r.Languages["go"]), []string{"1.21", "1.22"}) {
			t.Fatalf("go versions should be unchanged, got %v", versionKeys(r.Languages["go"]))
		}
	})

	t.Run("multiple unknown languages aggregate via errors.Join", func(t *testing.T) {
		r := newTestRegistry()
		err := r.Filter(imagespec.LangFilter{"py2": nil, "py3": nil})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "unknown language: py2") {
			t.Errorf("expected error to contain %q, got %v", "unknown language: py2", err)
		}
		if !strings.Contains(err.Error(), "unknown language: py3") {
			t.Errorf("expected error to contain %q, got %v", "unknown language: py3", err)
		}
		if !slices.Equal(langKeys(r), []string{"go", "python"}) {
			t.Fatalf("registry should be unchanged, got languages %v", langKeys(r))
		}
	})
}
