// Package notes writes a release's notes: which issues and commits each
// changed build brings, grouped by what kind of change they are.
//
// For every service whose build changed it reads the two images' revision
// labels, lists the commits between them in the service's repo, collects the
// CON-NN ids at the start of their subjects, and looks each one up in Linear.
// Repos use "CON-NN: …" subjects, not Conventional Commits.
package notes

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ogen-app/ogen-release/internal/github"
	"github.com/ogen-app/ogen-release/internal/linear"
)

// Change is one service's build between two releases. Presence is tracked on
// its own, since 0 is a valid build.
type Change struct {
	Service        string
	Image, Repo    string
	From, To       int
	Added, Removed bool
}

// Sources is what the notes are read from.
type Sources interface {
	Revision(ctx context.Context, image, tag string) (string, error)
	Commits(ctx context.Context, repo, base, head string) ([]github.Commit, error)
	Issue(ctx context.Context, identifier string) (linear.Issue, error)
}

// Entry is one issue in the notes and the services it changed.
type Entry struct {
	Issue    linear.Issue // only Identifier is set if Linear doesn't know it
	Services []string
}

// Commit is a commit without a CON id.
type Commit struct {
	Service string
	github.Commit
}

// Notes is what a release brings beyond its build table.
type Notes struct {
	Features, Improvements, Fixes, Other []Entry
	Commits                              []Commit
}

// Issues returns every issue in the notes that Linear knows.
func (n Notes) Issues() []linear.Issue {
	var out []linear.Issue
	for _, group := range [][]Entry{n.Features, n.Improvements, n.Fixes, n.Other} {
		for _, e := range group {
			if e.Issue.ID != "" {
				out = append(out, e.Issue)
			}
		}
	}
	return out
}

// idsRE matches the CON ids a subject starts with: "CON-1: …", "CON-1, CON-2: …".
var idsRE = regexp.MustCompile(`^CON-\d+(?:\s*,\s*CON-\d+)*`)

// Collect reads the notes for the changed builds. Added and removed services
// have no earlier or later build to compare, so they appear only in the build
// table.
func Collect(ctx context.Context, src Sources, changes []Change) (Notes, error) {
	services := map[string][]string{} // CON id → services, in first-seen order
	var ids []string
	var n Notes

	for _, c := range changes {
		if c.Added || c.Removed {
			continue
		}
		from, err := src.Revision(ctx, c.Image, strconv.Itoa(c.From))
		if err != nil {
			return Notes{}, fmt.Errorf("%s: %w", c.Service, err)
		}
		to, err := src.Revision(ctx, c.Image, strconv.Itoa(c.To))
		if err != nil {
			return Notes{}, fmt.Errorf("%s: %w", c.Service, err)
		}
		commits, err := src.Commits(ctx, c.Repo, from, to)
		if err != nil {
			return Notes{}, fmt.Errorf("%s: %w", c.Service, err)
		}
		for _, cm := range commits {
			found := idsRE.FindString(cm.Subject)
			if found == "" {
				n.Commits = append(n.Commits, Commit{Service: c.Service, Commit: cm})
				continue
			}
			for id := range strings.SplitSeq(found, ",") {
				id = strings.TrimSpace(id)
				if _, seen := services[id]; !seen {
					ids = append(ids, id)
				}
				if !slices.Contains(services[id], c.Service) {
					services[id] = append(services[id], c.Service)
				}
			}
		}
	}

	slices.SortFunc(ids, func(a, b string) int { return cmp.Compare(number(a), number(b)) })
	for _, id := range ids {
		is, err := src.Issue(ctx, id)
		if errors.Is(err, linear.ErrNotFound) {
			is = linear.Issue{Identifier: id}
		} else if err != nil {
			return Notes{}, err
		}
		e := Entry{Issue: is, Services: services[id]}
		switch {
		case is.HasLabel("Feature"):
			n.Features = append(n.Features, e)
		case is.HasLabel("Improvement"):
			n.Improvements = append(n.Improvements, e)
		case is.HasLabel("Bug"):
			n.Fixes = append(n.Fixes, e)
		default:
			n.Other = append(n.Other, e)
		}
	}
	return n, nil
}

func number(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "CON-"))
	return n
}

// Render writes a release's section of CHANGELOG.md, which is also the Release
// PR body and the GitHub Release text. first marks Ogen 1, which has no earlier
// release to compare with.
func Render(version int, first bool, changes []Change, n Notes) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Ogen %d\n\n", version)
	if first {
		b.WriteString("First release.\n\n")
	}
	for _, s := range []struct {
		title   string
		entries []Entry
	}{
		{"Features", n.Features},
		{"Improvements", n.Improvements},
		{"Fixes", n.Fixes},
		{"Other", n.Other},
	} {
		if len(s.entries) == 0 && (s.title != "Other" || len(n.Commits) == 0) {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", s.title)
		for _, e := range s.entries {
			fmt.Fprintf(&b, "- %s · %s\n", issueLine(e.Issue), strings.Join(e.Services, ", "))
		}
		if s.title == "Other" {
			for _, c := range n.Commits {
				fmt.Fprintf(&b, "- [`%s`](%s) %s · %s\n", c.SHA[:min(7, len(c.SHA))], c.URL, c.Subject, c.Service)
			}
		}
		b.WriteString("\n")
	}

	if !first && len(changes) > 0 {
		b.WriteString("### Builds\n\n")
	}
	b.WriteString("| Service | Build |\n| --- | --- |\n")
	for _, c := range changes {
		fmt.Fprintf(&b, "| %s | %s |\n", c.Service, buildCell(c, first))
	}
	return b.String()
}

func issueLine(is linear.Issue) string {
	if is.URL == "" {
		return is.Identifier
	}
	return fmt.Sprintf("[%s](%s) %s", is.Identifier, is.URL, is.Title)
}

func buildCell(c Change, first bool) string {
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
