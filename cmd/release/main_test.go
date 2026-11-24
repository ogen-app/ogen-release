package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	current = `services:
  pdf-service: { image: ghcr.io/ogen-app/pdf-service, build: 18, repo: ogen-app/pdf-service }
  api: { image: ghcr.io/ogen-app/ogen, build: 1552, repo: ogen-app/ogen }
  ui: { image: ghcr.io/ogen-app/ui, build: 994, repo: ogen-app/ui }
`
	previous = `services:
  pdf-service: { image: ghcr.io/ogen-app/pdf-service, build: 18, repo: ogen-app/pdf-service }
  api: { image: ghcr.io/ogen-app/ogen, build: 1551, repo: ogen-app/ogen }
  harbor: { image: ghcr.io/ogen-app/harbor, build: 242, repo: ogen-app/harbor }
`
)

// workdir writes release.yaml, VERSION and optionally the previous release and
// CHANGELOG.md, and returns options pointing at them.
func workdir(t *testing.T, version, prev, changelog string) prOptions {
	t.Helper()
	dir := t.TempDir()
	write := func(name, data string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	o := prOptions{
		releaseFile:   write("release.yaml", current),
		versionFile:   write("VERSION", version),
		changelogFile: filepath.Join(dir, "CHANGELOG.md"),
		bodyFile:      filepath.Join(dir, "body.md"),
	}
	if prev != "" {
		o.previousFile = write("previous.yaml", prev)
	}
	if changelog != "" {
		write("CHANGELOG.md", changelog)
	}
	return o
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFirstRelease(t *testing.T) {
	o := workdir(t, "0\n", "", "")
	out, err := pr(o)
	if err != nil {
		t.Fatal(err)
	}
	if out != "version=1\nchanged=true\n" {
		t.Errorf("output = %q", out)
	}
	if got := read(t, o.versionFile); got != "1\n" {
		t.Errorf("VERSION = %q", got)
	}
	want := "# Changelog\n\n## Ogen 1\n\nFirst release.\n\n| Service | Build |\n| --- | --- |\n" +
		"| pdf-service | 18 |\n| api | 1552 |\n| ui | 994 |\n"
	if got := read(t, o.changelogFile); got != want {
		t.Errorf("CHANGELOG.md:\n%s\nwant:\n%s", got, want)
	}
	body := read(t, o.bodyFile)
	if !strings.HasPrefix(body, "## Ogen 1\n") || !strings.Contains(body, "tags `ogen-1`") {
		t.Errorf("body = %q", body)
	}
}

// Only what changed since the last release is listed: a new build, a new
// service and a dropped one; an unchanged service is left out.
func TestNextRelease(t *testing.T) {
	older := "# Changelog\n\n## Ogen 1\n\nFirst release.\n"
	o := workdir(t, "1\n", previous, older)
	out, err := pr(o)
	if err != nil {
		t.Fatal(err)
	}
	if out != "version=2\nchanged=true\n" {
		t.Errorf("output = %q", out)
	}
	want := "# Changelog\n\n## Ogen 2\n\n| Service | Build |\n| --- | --- |\n" +
		"| api | 1551 → 1552 |\n| ui | 994 (new) |\n| harbor | 242 → removed |\n" +
		"\n## Ogen 1\n\nFirst release.\n"
	if got := read(t, o.changelogFile); got != want {
		t.Errorf("CHANGELOG.md:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(read(t, o.bodyFile), "pdf-service") {
		t.Error("unchanged pdf-service is listed")
	}
}

func TestNothingToRelease(t *testing.T) {
	o := workdir(t, "1\n", current, "# Changelog\n\n## Ogen 1\n")
	out, err := pr(o)
	if err != nil {
		t.Fatal(err)
	}
	if out != "version=2\nchanged=false\n" {
		t.Errorf("output = %q", out)
	}
	if got := read(t, o.versionFile); got != "1\n" {
		t.Errorf("VERSION changed to %q", got)
	}
	if _, err := os.Stat(o.bodyFile); err == nil {
		t.Error("wrote a PR body with nothing to release")
	}
}

// A body that can't be written must fail before VERSION or CHANGELOG.md
// changes, or a retry would look for a release that never happened.
func TestUnwritableBodyLeavesReleaseFilesAlone(t *testing.T) {
	older := "# Changelog\n\n## Ogen 1\n\nFirst release.\n"
	o := workdir(t, "1\n", previous, older)
	o.bodyFile = filepath.Join(t.TempDir(), "missing-dir", "body.md")
	if _, err := pr(o); err == nil {
		t.Fatal("want error")
	}
	if got := read(t, o.versionFile); got != "1\n" {
		t.Errorf("VERSION changed to %q", got)
	}
	if got := read(t, o.changelogFile); got != older {
		t.Errorf("CHANGELOG.md changed to %q", got)
	}
}

// 0 is a valid build: moving to or from it is an ordinary change, not a
// service appearing or disappearing.
func TestBuildZeroIsABuild(t *testing.T) {
	prev := "services:\n  api: { image: i, build: 0, repo: r }\n  ui: { image: i, build: 7, repo: r }\n"
	cur := "services:\n  api: { image: i, build: 3, repo: r }\n  ui: { image: i, build: 0, repo: r }\n"
	o := workdir(t, "1\n", prev, "")
	if err := os.WriteFile(o.releaseFile, []byte(cur), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pr(o); err != nil {
		t.Fatal(err)
	}
	notes := read(t, o.changelogFile)
	for _, want := range []string{"| api | 0 → 3 |", "| ui | 7 → 0 |"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if strings.Contains(notes, "(new)") || strings.Contains(notes, "removed") {
		t.Errorf("a build of 0 was read as a new or removed service:\n%s", notes)
	}
}

func TestPRRejectsBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		version, prev string
		noBody        bool
	}{
		"not a number":           {version: "one\n"},
		"negative":               {version: "-1\n"},
		"released, no previous":  {version: "1\n"},
		"no body path":           {version: "0\n", noBody: true},
		"previous doesn't parse": {version: "1\n", prev: "services: {}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			o := workdir(t, tc.version, tc.prev, "")
			if tc.noBody {
				o.bodyFile = ""
			}
			if _, err := pr(o); err == nil {
				t.Fatal("want error")
			}
			if got := read(t, o.versionFile); got != tc.version {
				t.Errorf("VERSION changed to %q", got)
			}
		})
	}
}
