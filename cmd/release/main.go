// Command release prepares and records product releases, Ogen N.
//
// Versions are plain integers (Ogen 1, Ogen 2, …), tagged ogen-N. VERSION
// holds the last released N; 0 means nothing has been released. All three
// subcommands are run by .github/workflows/release.yml.
//
//	release pr    -previous <release.yaml at ogen-N> -body <file>
//	release notes -version <N>
//	release mark  -previous <release.yaml at ogen-(N-1)> -url <GitHub Release>
//
// pr runs on every push to main. It compares release.yaml with the one the
// last release shipped and, if any build changed, writes VERSION = N+1,
// prepends that release's notes to CHANGELOG.md and writes the Release PR body.
// It prints `version=` and `changed=` lines for $GITHUB_OUTPUT. With nothing
// changed it writes no file.
//
// notes prints release N's section of CHANGELOG.md, for the GitHub Release.
//
// mark records release N in Linear: every shipped issue gets the label "Ogen N"
// and a comment linking the GitHub Release. Issues already labelled are
// skipped, so it can be re-run.
//
// The notes read GHCR (anonymously), GitHub (GITHUB_TOKEN) and Linear
// (LINEAR_API_KEY).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/ogen-app/ogen-release/internal/ghcr"
	"github.com/ogen-app/ogen-release/internal/github"
	"github.com/ogen-app/ogen-release/internal/linear"
	"github.com/ogen-app/ogen-release/internal/notes"
	"github.com/ogen-app/ogen-release/internal/release"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("release: ")
	if len(os.Args) < 2 {
		log.Fatal("usage: release pr|notes|mark [flags]")
	}
	ctx := context.Background()

	var (
		out string
		err error
	)
	switch os.Args[1] {
	case "pr":
		fs := flag.NewFlagSet("pr", flag.ExitOnError)
		o := prOptions{}
		fs.StringVar(&o.releaseFile, "release", "release.yaml", "path to release.yaml")
		fs.StringVar(&o.previousFile, "previous", "", "path to release.yaml as of the last release; empty before Ogen 1")
		fs.StringVar(&o.versionFile, "version", "VERSION", "path to VERSION")
		fs.StringVar(&o.changelogFile, "changelog", "CHANGELOG.md", "path to CHANGELOG.md")
		fs.StringVar(&o.bodyFile, "body", "", "where to write the Release PR body")
		_ = fs.Parse(os.Args[2:])
		o.sources = newSources()
		out, err = pr(ctx, o)
	case "notes":
		fs := flag.NewFlagSet("notes", flag.ExitOnError)
		changelog := fs.String("changelog", "CHANGELOG.md", "path to CHANGELOG.md")
		version := fs.Int("version", 0, "release whose notes to print")
		_ = fs.Parse(os.Args[2:])
		out, err = releaseNotes(*changelog, *version)
	case "mark":
		fs := flag.NewFlagSet("mark", flag.ExitOnError)
		o := markOptions{}
		fs.StringVar(&o.releaseFile, "release", "release.yaml", "path to release.yaml")
		fs.StringVar(&o.previousFile, "previous", "", "path to release.yaml as of the release before; empty for Ogen 1")
		fs.StringVar(&o.versionFile, "version", "VERSION", "path to VERSION")
		fs.StringVar(&o.url, "url", "", "the GitHub Release the comments link to")
		_ = fs.Parse(os.Args[2:])
		src := newSources()
		o.sources, o.marker = src, src.linear
		out, err = mark(ctx, o)
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(out)
}

// sources reads the notes from the real services.
type sources struct {
	ghcr   *ghcr.Client
	github *github.Client
	linear *linear.Client
}

func newSources() *sources {
	return &sources{
		ghcr:   &ghcr.Client{},
		github: &github.Client{Token: os.Getenv("GITHUB_TOKEN")},
		linear: &linear.Client{APIKey: os.Getenv("LINEAR_API_KEY")},
	}
}

func (s *sources) Revision(ctx context.Context, image, tag string) (string, error) {
	return s.ghcr.Revision(ctx, image, tag)
}

func (s *sources) Commits(ctx context.Context, repo, base, head string) ([]github.Commit, error) {
	return s.github.Commits(ctx, repo, base, head)
}

func (s *sources) Issue(ctx context.Context, identifier string) (linear.Issue, error) {
	if s.linear.APIKey == "" {
		return linear.Issue{}, errors.New("LINEAR_API_KEY is not set")
	}
	return s.linear.Issue(ctx, identifier)
}

type prOptions struct {
	releaseFile, previousFile, versionFile, changelogFile, bodyFile string
	sources                                                         notes.Sources
}

// pr writes the next release and returns its $GITHUB_OUTPUT lines.
func pr(ctx context.Context, o prOptions) (string, error) {
	if o.bodyFile == "" {
		return "", errors.New("-body is required")
	}
	last, err := readVersion(o.versionFile)
	if err != nil {
		return "", err
	}
	next := last + 1

	prev, cur, err := releases(o.releaseFile, o.previousFile, last)
	if err != nil {
		return "", err
	}
	changes := diff(prev, cur)
	if prev != nil && len(changes) == 0 {
		return output(next, false), nil
	}

	var n notes.Notes
	if prev != nil {
		if n, err = notes.Collect(ctx, o.sources, changes); err != nil {
			return "", fmt.Errorf("release notes: %w", err)
		}
	}

	// Everything is prepared before any write, and VERSION is written last: a
	// run that fails part-way never leaves VERSION claiming a release whose
	// notes weren't written.
	section := notes.Render(next, prev == nil, changes, n)
	changelog, err := prependChangelog(o.changelogFile, section)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(o.bodyFile, []byte(renderBody(next, section)), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(o.changelogFile, []byte(changelog), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(o.versionFile, []byte(strconv.Itoa(next)+"\n"), 0o644); err != nil {
		return "", err
	}
	return output(next, true), nil
}

// releases parses the current release.yaml and, after Ogen 1, the one the
// release before shipped.
func releases(releaseFile, previousFile string, last int) (prev, cur *release.File, err error) {
	if cur, err = parseFile(releaseFile); err != nil {
		return nil, nil, err
	}
	switch {
	case previousFile != "":
		if prev, err = parseFile(previousFile); err != nil {
			return nil, nil, fmt.Errorf("previous release: %w", err)
		}
	case last > 0:
		return nil, nil, fmt.Errorf("VERSION says Ogen %d was released, but no release.yaml from ogen-%d was given", last, last)
	}
	return prev, cur, nil
}

func output(version int, changed bool) string {
	return fmt.Sprintf("version=%d\nchanged=%t\n", version, changed)
}

func readVersion(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must hold a non-negative integer, got %q", path, strings.TrimSpace(string(data)))
	}
	return n, nil
}

func parseFile(path string) (*release.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return release.Parse(data)
}

// diff lists the services whose build differs, in the current file's order,
// then any the previous release had and this one doesn't. Without a previous
// release every service is added.
func diff(prev, cur *release.File) []notes.Change {
	var out []notes.Change
	for _, s := range cur.Services {
		c := notes.Change{Service: s.Name, Image: s.Image, Repo: s.Repo, To: s.Build, Added: true}
		if prev != nil {
			if p, ok := prev.Service(s.Name); ok {
				if p.Build == s.Build {
					continue
				}
				c.From, c.Added = p.Build, false
			}
		}
		out = append(out, c)
	}
	if prev != nil {
		for _, p := range prev.Services {
			if _, ok := cur.Service(p.Name); !ok {
				out = append(out, notes.Change{Service: p.Name, Image: p.Image, Repo: p.Repo, From: p.Build, Removed: true})
			}
		}
	}
	return out
}

func renderBody(version int, section string) string {
	return section + fmt.Sprintf("\nMerging this PR releases **Ogen %d**: it tags `ogen-%d`, and production deploys "+
		"after approval. It can merge only while `development-smoke` is green on the `main` commit it was built on.\n",
		version, version)
}

const changelogHeader = "# Changelog\n\n"

// prependChangelog returns the changelog with section first, under the file's
// header, and every earlier release below it. It doesn't write the file.
func prependChangelog(path, section string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	rest := strings.TrimPrefix(string(data), changelogHeader)
	out := changelogHeader + section
	if strings.TrimSpace(rest) != "" {
		out += "\n" + rest
	}
	return out, nil
}

// releaseNotes returns release version's section of the changelog without its
// heading, which the GitHub Release shows as its title.
func releaseNotes(changelogFile string, version int) (string, error) {
	if version <= 0 {
		return "", errors.New("-version must be a released Ogen number")
	}
	data, err := os.ReadFile(changelogFile)
	if err != nil {
		return "", err
	}
	heading := fmt.Sprintf("## Ogen %d\n", version)
	_, section, found := strings.Cut(string(data), heading)
	if !found {
		return "", fmt.Errorf("%s has no section for Ogen %d", changelogFile, version)
	}
	if end := strings.Index(section, "\n## Ogen "); end >= 0 {
		section = section[:end+1]
	}
	return strings.TrimSpace(section) + "\n", nil
}

// marker records a release on an issue.
type marker interface {
	ReleaseLabel(ctx context.Context, name string) (string, error)
	MarkShipped(ctx context.Context, issueID, labelID, comment string) error
}

type markOptions struct {
	releaseFile, previousFile, versionFile, url string
	sources                                     notes.Sources
	marker                                      marker
}

// mark labels and comments on every issue release N shipped. It returns a
// summary line per issue.
func mark(ctx context.Context, o markOptions) (string, error) {
	if o.url == "" {
		return "", errors.New("-url is required")
	}
	version, err := readVersion(o.versionFile)
	if err != nil {
		return "", err
	}
	if version == 0 {
		return "", errors.New("VERSION is 0: nothing has been released")
	}
	prev, cur, err := releases(o.releaseFile, o.previousFile, version-1)
	if err != nil {
		return "", err
	}
	if prev == nil {
		return fmt.Sprintf("Ogen %d is the first release; there are no issues to mark.\n", version), nil
	}
	n, err := notes.Collect(ctx, o.sources, diff(prev, cur))
	if err != nil {
		return "", fmt.Errorf("release notes: %w", err)
	}
	issues := n.Issues()
	if len(issues) == 0 {
		return fmt.Sprintf("Ogen %d ships no Linear issues.\n", version), nil
	}

	label := fmt.Sprintf("Ogen %d", version)
	labelID, err := o.marker.ReleaseLabel(ctx, label)
	if err != nil {
		return "", err
	}
	comment := fmt.Sprintf("Shipped in **%s**: %s", label, o.url)
	var b strings.Builder
	for _, is := range issues {
		if is.HasLabel(label) {
			fmt.Fprintf(&b, "%s: already marked\n", is.Identifier)
			continue
		}
		if err := o.marker.MarkShipped(ctx, is.ID, labelID, comment); err != nil {
			return b.String(), fmt.Errorf("%s: %w", is.Identifier, err)
		}
		fmt.Fprintf(&b, "%s: marked %s\n", is.Identifier, label)
	}
	return b.String(), nil
}
