package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sample = "services:\n  api:\n    image: ghcr.io/ogen-app/ogen\n    build: 100\n    repo: ogen-app/ogen\n"
	sha    = "9d286386aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func writeSample(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.yaml")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBump(t *testing.T) {
	path := writeSample(t)

	msg, err := run(path, "api", 101, sha)
	if err != nil {
		t.Fatal(err)
	}
	if want := "fix(deps): api 100 → 101\n\nRevision: " + sha + "\n"; msg != want {
		t.Errorf("msg = %q, want %q", msg, want)
	}
	got, _ := os.ReadFile(path)
	if want := strings.Replace(sample, "build: 100", "build: 101", 1); string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestBumpIgnoresOlderOrSameBuild(t *testing.T) {
	for _, build := range []int{99, 100} {
		path := writeSample(t)
		msg, err := run(path, "api", build, sha)
		if err != nil {
			t.Fatal(err)
		}
		if msg != "" {
			t.Errorf("build %d: msg = %q, want no-op", build, msg)
		}
		if got, _ := os.ReadFile(path); string(got) != sample {
			t.Errorf("build %d: file changed", build)
		}
	}
}

// ui publishes and dispatches before it joins release.yaml; its bumps must not
// fail the workflow or touch the file.
func TestBumpIgnoresServiceNotInReleaseYAML(t *testing.T) {
	path := writeSample(t)
	msg, err := run(path, "ui", 101, sha)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "" {
		t.Errorf("msg = %q, want no-op", msg)
	}
	if got, _ := os.ReadFile(path); string(got) != sample {
		t.Error("file changed")
	}
}

func TestBumpRejectsBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		service, revision string
		build             int
	}{
		"zero build":     {"api", sha, 0},
		"short revision": {"api", "9d286386", 101},
		"bad revision":   {"api", "$(whoami)" + strings.Repeat("a", 31), 101},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeSample(t)
			if _, err := run(path, tc.service, tc.build, tc.revision); err == nil {
				t.Error("want error")
			}
			if got, _ := os.ReadFile(path); string(got) != sample {
				t.Error("file changed")
			}
		})
	}
}
