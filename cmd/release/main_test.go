package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ogen-app/ogen-release/internal/github"
	"github.com/ogen-app/ogen-release/internal/linear"
	"github.com/ogen-app/ogen-release/internal/release"
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

// fakeSources serves api 1551 → 1552 as two commits: CON-530 (a feature) and
// one without an id.
type fakeSources struct {
	issues map[string]linear.Issue
}

func newFakeSources() *fakeSources {
	return &fakeSources{issues: map[string]linear.Issue{
		"CON-530": {ID: "uuid-530", Identifier: "CON-530", Title: "The thing", URL: "https://linear.app/i/CON-530", Labels: []string{"Feature"}},
	}}
}

func (f *fakeSources) Revision(_ context.Context, image, tag string) (string, error) {
	return image + "@" + tag, nil
}

func (f *fakeSources) Commits(_ context.Context, repo, base, head string) ([]github.Commit, error) {
	if repo != "ogen-app/ogen" || base != "ghcr.io/ogen-app/ogen@1551" || head != "ghcr.io/ogen-app/ogen@1552" {
		return nil, fmt.Errorf("unexpected compare %s %s...%s", repo, base, head)
	}
	return []github.Commit{
		{SHA: "aaaaaaa1", Subject: "CON-530: add the thing", URL: "https://github.com/c/1"},
		{SHA: "aaaaaaa2", Subject: "Tidy the README", URL: "https://github.com/c/2"},
	}, nil
}

func (f *fakeSources) Issue(_ context.Context, id string) (linear.Issue, error) {
	is, ok := f.issues[id]
	if !ok {
		return linear.Issue{}, fmt.Errorf("%s: %w", id, linear.ErrNotFound)
	}
	return is, nil
}

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
		sources:       newFakeSources(),
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
	o.sources = nil // Ogen 1 reads no notes
	out, err := pr(t.Context(), o)
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

// Only what changed since the last release is listed: a new build with its
// issues and commits, a new service and a dropped one.
func TestNextRelease(t *testing.T) {
	older := "# Changelog\n\n## Ogen 1\n\nFirst release.\n"
	o := workdir(t, "1\n", previous, older)
	out, err := pr(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if out != "version=2\nchanged=true\n" {
		t.Errorf("output = %q", out)
	}
	got := read(t, o.changelogFile)
	for _, want := range []string{
		"# Changelog\n\n## Ogen 2\n\n### Features\n\n- [CON-530](https://linear.app/i/CON-530) The thing · api\n",
		"### Other\n\n- [`aaaaaaa`](https://github.com/c/2) Tidy the README · api\n",
		"### Builds\n\n| Service | Build |\n| --- | --- |\n| api | 1551 → 1552 |\n| ui | 994 (new) |\n| harbor | 242 → removed |\n",
		"\n## Ogen 1\n\nFirst release.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CHANGELOG.md lacks:\n%s\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(read(t, o.bodyFile), "pdf-service") {
		t.Error("unchanged pdf-service is listed")
	}
}

func TestNothingToRelease(t *testing.T) {
	o := workdir(t, "1\n", current, "# Changelog\n\n## Ogen 1\n")
	out, err := pr(t.Context(), o)
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
	if _, err := pr(t.Context(), o); err == nil {
		t.Fatal("want error")
	}
	if got := read(t, o.versionFile); got != "1\n" {
		t.Errorf("VERSION changed to %q", got)
	}
	if got := read(t, o.changelogFile); got != older {
		t.Errorf("CHANGELOG.md changed to %q", got)
	}
}

// Notes that can't be read must fail the run before any file changes.
func TestNotesFailureLeavesReleaseFilesAlone(t *testing.T) {
	o := workdir(t, "1\n", strings.Replace(previous, "build: 1551", "build: 1500", 1), "")
	if _, err := pr(t.Context(), o); err == nil || !strings.Contains(err.Error(), "release notes") {
		t.Fatalf("err = %v", err)
	}
	if got := read(t, o.versionFile); got != "1\n" {
		t.Errorf("VERSION changed to %q", got)
	}
}

// 0 is a valid build: moving to or from it is an ordinary change, not a
// service appearing or disappearing.
func TestBuildZeroIsABuild(t *testing.T) {
	prev := "services:\n  api: { image: i, build: 0, repo: r }\n  ui: { image: i, build: 7, repo: r }\n"
	cur := "services:\n  api: { image: i, build: 3, repo: r }\n  ui: { image: i, build: 0, repo: r }\n"
	changes := diff(parse(t, prev), parse(t, cur))
	if len(changes) != 2 || changes[0].Added || changes[0].From != 0 || changes[0].To != 3 ||
		changes[1].Removed || changes[1].From != 7 || changes[1].To != 0 {
		t.Errorf("changes = %+v", changes)
	}
}

func parse(t *testing.T, data string) *release.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.yaml")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := parseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
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
			if _, err := pr(t.Context(), o); err == nil {
				t.Fatal("want error")
			}
			if got := read(t, o.versionFile); got != tc.version {
				t.Errorf("VERSION changed to %q", got)
			}
		})
	}
}

func TestReleaseNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	data := "# Changelog\n\n## Ogen 2\n\n### Features\n\n- CON-530 · api\n\n## Ogen 1\n\nFirst release.\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	for version, want := range map[int]string{
		2: "### Features\n\n- CON-530 · api\n",
		1: "First release.\n",
	} {
		got, err := releaseNotes(path, version)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("Ogen %d notes = %q, want %q", version, got, want)
		}
	}
	if _, err := releaseNotes(path, 3); err == nil {
		t.Error("Ogen 3: want error")
	}
}

// fakeMarker records what mark does in Linear.
type fakeMarker struct {
	labels []string
	marked []string
}

func (m *fakeMarker) ReleaseLabel(_ context.Context, name string) (string, error) {
	m.labels = append(m.labels, name)
	return "label-" + name, nil
}

func (m *fakeMarker) MarkShipped(_ context.Context, issueID, labelID, comment string) error {
	m.marked = append(m.marked, issueID+" "+labelID+" "+comment)
	return nil
}

func markWorkdir(t *testing.T) markOptions {
	t.Helper()
	o := workdir(t, "2\n", previous, "")
	return markOptions{
		releaseFile:  o.releaseFile,
		previousFile: o.previousFile,
		versionFile:  o.versionFile,
		url:          "https://github.com/ogen-app/ogen-release/releases/tag/ogen-2",
		sources:      newFakeSources(),
		marker:       &fakeMarker{},
	}
}

func TestMark(t *testing.T) {
	o := markWorkdir(t)
	out, err := mark(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	m := o.marker.(*fakeMarker)
	if strings.Join(m.labels, ",") != "Ogen 2" {
		t.Errorf("labels = %v", m.labels)
	}
	want := "uuid-530 label-Ogen 2 Shipped in **Ogen 2**: https://github.com/ogen-app/ogen-release/releases/tag/ogen-2"
	if len(m.marked) != 1 || m.marked[0] != want {
		t.Errorf("marked = %q", m.marked)
	}
	if out != "CON-530: marked Ogen 2\n" {
		t.Errorf("output = %q", out)
	}
}

// Re-running a release skips issues that already carry its label.
func TestMarkSkipsMarkedIssues(t *testing.T) {
	o := markWorkdir(t)
	src := o.sources.(*fakeSources)
	is := src.issues["CON-530"]
	is.Labels = append(is.Labels, "Ogen 2")
	src.issues["CON-530"] = is

	out, err := mark(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if m := o.marker.(*fakeMarker); len(m.marked) != 0 {
		t.Errorf("marked again: %q", m.marked)
	}
	if out != "CON-530: already marked\n" {
		t.Errorf("output = %q", out)
	}
}

func TestMarkFirstRelease(t *testing.T) {
	o := markWorkdir(t)
	if err := os.WriteFile(o.versionFile, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.previousFile = ""
	out, err := mark(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "first release") || len(o.marker.(*fakeMarker).labels) != 0 {
		t.Errorf("output = %q", out)
	}
}
