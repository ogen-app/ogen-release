// Command release prepares the next product release, Ogen N.
//
// Versions are plain integers (Ogen 1, Ogen 2, …), tagged ogen-N. VERSION
// holds the last released N; 0 means nothing has been released.
//
//	release pr -previous <release.yaml at ogen-N> -body <file>
//
// pr is the body of .github/workflows/release.yml on every push to main. It
// compares release.yaml with the one the last release shipped and, if any build
// changed, writes VERSION = N+1, prepends that release's notes to CHANGELOG.md
// and writes the Release PR body. It prints `version=` and `changed=` lines for
// $GITHUB_OUTPUT. With nothing changed it writes no file.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/ogen-app/ogen-release/internal/release"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("release: ")

	if len(os.Args) < 2 || os.Args[1] != "pr" {
		log.Fatal("usage: release pr [flags]")
	}
	fs := flag.NewFlagSet("pr", flag.ExitOnError)
	opts := prOptions{}
	fs.StringVar(&opts.releaseFile, "release", "release.yaml", "path to release.yaml")
	fs.StringVar(&opts.previousFile, "previous", "", "path to release.yaml as of the last release; empty before Ogen 1")
	fs.StringVar(&opts.versionFile, "version", "VERSION", "path to VERSION")
	fs.StringVar(&opts.changelogFile, "changelog", "CHANGELOG.md", "path to CHANGELOG.md")
	fs.StringVar(&opts.bodyFile, "body", "", "where to write the Release PR body")
	_ = fs.Parse(os.Args[2:])

	out, err := pr(opts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(out)
}

type prOptions struct {
	releaseFile, previousFile, versionFile, changelogFile, bodyFile string
}

// pr writes the next release and returns its $GITHUB_OUTPUT lines.
func pr(o prOptions) (string, error) {
	if o.bodyFile == "" {
		return "", errors.New("-body is required")
	}
	last, err := readVersion(o.versionFile)
	if err != nil {
		return "", err
	}
	next := last + 1

	cur, err := parseFile(o.releaseFile)
	if err != nil {
		return "", err
	}
	var prev *release.File
	switch {
	case o.previousFile != "":
		if prev, err = parseFile(o.previousFile); err != nil {
			return "", fmt.Errorf("previous release: %w", err)
		}
	case last > 0:
		return "", fmt.Errorf("VERSION says Ogen %d was released, but no release.yaml from ogen-%d was given", last, last)
	}

	changes := diff(prev, cur)
	if prev != nil && len(changes) == 0 {
		return output(next, false), nil
	}

	// Everything is prepared before any write, and VERSION is written last: a
	// run that fails part-way never leaves VERSION claiming a release whose
	// notes weren't written.
	notes := renderNotes(next, prev == nil, changes)
	changelog, err := prependChangelog(o.changelogFile, notes)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(o.bodyFile, []byte(renderBody(next, notes)), 0o644); err != nil {
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

// change is one service's build between two releases. Presence is tracked on
// its own, since 0 is a valid build: Added means the previous release didn't
// have the service, Removed that this one drops it.
type change struct {
	Service        string
	From, To       int
	Added, Removed bool
}

// diff lists the services whose build differs, in the current file's order,
// then any the previous release had and this one doesn't. Without a previous
// release every service is added.
func diff(prev, cur *release.File) []change {
	var out []change
	for _, s := range cur.Services {
		c := change{Service: s.Name, To: s.Build, Added: true}
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
				out = append(out, change{Service: p.Name, From: p.Build, Removed: true})
			}
		}
	}
	return out
}

// renderNotes is the release's section in CHANGELOG.md and the core of the
// Release PR body.
func renderNotes(version int, first bool, changes []change) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Ogen %d\n\n", version)
	if first {
		b.WriteString("First release.\n\n")
	}
	b.WriteString("| Service | Build |\n| --- | --- |\n")
	for _, c := range changes {
		fmt.Fprintf(&b, "| %s | %s |\n", c.Service, buildCell(c, first))
	}
	return b.String()
}

func buildCell(c change, first bool) string {
	switch {
	case first:
		return strconv.Itoa(c.To)
	case c.Added:
		return strconv.Itoa(c.To) + " (new)"
	case c.Removed:
		return strconv.Itoa(c.From) + " → removed"
	default:
		return strconv.Itoa(c.From) + " → " + strconv.Itoa(c.To)
	}
}

func renderBody(version int, notes string) string {
	return notes + fmt.Sprintf("\nMerging this PR releases **Ogen %d**: it tags `ogen-%d`, and production deploys "+
		"after approval. It can merge only while `development-smoke` is green on the `main` commit it was built on.\n",
		version, version)
}

const changelogHeader = "# Changelog\n\n"

// prependChangelog returns the changelog with notes first, under the file's
// header, and every earlier release below it. It doesn't write the file.
func prependChangelog(path, notes string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	rest := strings.TrimPrefix(string(data), changelogHeader)
	out := changelogHeader + notes
	if strings.TrimSpace(rest) != "" {
		out += "\n" + rest
	}
	return out, nil
}
