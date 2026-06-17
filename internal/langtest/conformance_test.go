//go:build conformance

package langtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tpsawant027/runboxd/internal/sandbox"
	"github.com/tpsawant027/runboxd/internal/sandboxtest"
)

func TestConformance(t *testing.T) {
	sb := sandboxtest.NewTestSandboxFromEnv(t)
	imageDir := os.Getenv("IMAGES_DIR")
	if imageDir == "" {
		imageDir = "../../images"
	}
	fixtureGlob := filepath.Join(imageDir, "/*/tests.yml")
	fixtures, err := Load(fixtureGlob)
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	for _, fx := range fixtures {
		t.Run(fx.Language+"-"+fx.Version, func(t *testing.T) {
			if _, err := sb.LangSpec(fx.Language, fx.Version); err != nil {
				t.Skipf("language/version not supported by sandbox: %v", err)
			}
			sandboxtest.SkipUnsupportedOnNsjail(t, fx.Language)
			// The oom case asserts an actual OOM-kill, which needs a backend
			// that can enforce the memory cap (docker swap limits / nsjail cgroups).
			// Where it can't, skip oom rather than fail it.
			if _, ok := fx.ConformanceTests["oom"]; ok && !sandbox.MemoryLimitEnforced(t.Context(), sb) {
				t.Log("skipping oom conformance: backend cannot enforce memory limits on this host")
				delete(fx.ConformanceTests, "oom")
			}
			for _, r := range RunFixture(t.Context(), sb, fx) {
				if !r.Passed {
					t.Errorf("%s: %s", r.Name, r.Detail)
				}
			}
		})
	}
}
