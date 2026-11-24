package notes

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ogen-app/ogen-release/internal/github"
	"github.com/ogen-app/ogen-release/internal/linear"
)

// fake serves revisions as "<image>@<tag>", commits per repo and range, and
// issues from a map.
type fake struct {
	commits map[string][]github.Commit // "repo base...head"
	issues  map[string]linear.Issue
	lookups []string
}

func (f *fake) Revision(_ context.Context, image, tag string) (string, error) {
	return image + "@" + tag, nil
}

func (f *fake) Commits(_ context.Context, repo, base, head string) ([]github.Commit, error) {
	key := repo + " " + base + "..." + head
	cs, ok := f.commits[key]
	if !ok {
		return nil, fmt.Errorf("unexpected compare %s", key)
	}
	return cs, nil
}

func (f *fake) Issue(_ context.Context, id string) (linear.Issue, error) {
	f.lookups = append(f.lookups, id)
	is, ok := f.issues[id]
	if !ok {
		return linear.Issue{}, fmt.Errorf("%s: %w", id, linear.ErrNotFound)
	}
	return is, nil
}

func commit(sha, subject string) github.Commit {
	return github.Commit{SHA: sha, Subject: subject, URL: "https://github.com/c/" + sha}
}

func issue(id, title string, labels ...string) linear.Issue {
	return linear.Issue{ID: "uuid-" + id, Identifier: id, Title: title, URL: "https://linear.app/ogen/issue/" + id, Labels: labels}
}

var changes = []Change{
	{Service: "api", Image: "ghcr.io/ogen-app/ogen", Repo: "ogen-app/ogen", From: 1552, To: 1560},
	{Service: "ui", Image: "ghcr.io/ogen-app/ui", Repo: "ogen-app/ui", From: 994, To: 1001},
	{Service: "harbor", Image: "ghcr.io/ogen-app/harbor", Repo: "ogen-app/harbor", To: 250, Added: true},
}

func sources() *fake {
	return &fake{
		commits: map[string][]github.Commit{
			"ogen-app/ogen ghcr.io/ogen-app/ogen@1552...ghcr.io/ogen-app/ogen@1560": {
				commit("aaaaaaa1", "CON-530: add the thing"),
				commit("aaaaaaa2", "CON-531, CON-530: fix and follow-up"),
				commit("aaaaaaa3", "Tidy the README"),
			},
			"ogen-app/ui ghcr.io/ogen-app/ui@994...ghcr.io/ogen-app/ui@1001": {
				commit("bbbbbbb1", "CON-530: the thing's screen"),
				commit("bbbbbbb2", "CON-9999: an id Linear doesn't know"),
				commit("bbbbbbb3", "Mention CON-1 later in the subject"),
			},
		},
		issues: map[string]linear.Issue{
			"CON-530": issue("CON-530", "The thing", "Feature"),
			"CON-531": issue("CON-531", "The bug", "Bug"),
		},
	}
}

func TestCollect(t *testing.T) {
	src := sources()
	n, err := Collect(t.Context(), src, changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Features) != 1 || n.Features[0].Issue.Identifier != "CON-530" ||
		strings.Join(n.Features[0].Services, ",") != "api,ui" {
		t.Errorf("features = %+v", n.Features)
	}
	if len(n.Fixes) != 1 || n.Fixes[0].Issue.Identifier != "CON-531" {
		t.Errorf("fixes = %+v", n.Fixes)
	}
	if len(n.Other) != 1 || n.Other[0].Issue.Identifier != "CON-9999" || n.Other[0].Issue.ID != "" {
		t.Errorf("other = %+v", n.Other)
	}
	// An id later in the subject doesn't count; such commits are listed.
	if len(n.Commits) != 2 || n.Commits[0].Subject != "Tidy the README" || n.Commits[1].Service != "ui" {
		t.Errorf("commits = %+v", n.Commits)
	}
	// Each issue is looked up once, in id order.
	if got := strings.Join(src.lookups, ","); got != "CON-530,CON-531,CON-9999" {
		t.Errorf("lookups = %s", got)
	}
	// Only issues Linear knows are marked shipped.
	if got := n.Issues(); len(got) != 2 {
		t.Errorf("Issues() = %+v", got)
	}
}

func TestRender(t *testing.T) {
	n, err := Collect(t.Context(), sources(), changes)
	if err != nil {
		t.Fatal(err)
	}
	got := Render(2, false, changes, n)
	for _, want := range []string{
		"## Ogen 2\n\n### Features\n\n- [CON-530](https://linear.app/ogen/issue/CON-530) The thing · api, ui\n",
		"### Fixes\n\n- [CON-531](https://linear.app/ogen/issue/CON-531) The bug · api\n",
		"### Other\n\n- CON-9999 · ui\n- [`aaaaaaa`](https://github.com/c/aaaaaaa3) Tidy the README · api\n",
		"### Builds\n\n| Service | Build |\n| --- | --- |\n| api | 1552 → 1560 |\n| ui | 994 → 1001 |\n| harbor | 250 (new) |\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notes lack:\n%s\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(got, "### Improvements") {
		t.Error("rendered an empty section")
	}
}

func TestRenderFirstRelease(t *testing.T) {
	first := []Change{{Service: "api", To: 1552, Added: true}, {Service: "ui", To: 994, Added: true}}
	got := Render(1, true, first, Notes{})
	want := "## Ogen 1\n\nFirst release.\n\n| Service | Build |\n| --- | --- |\n| api | 1552 |\n| ui | 994 |\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCollectFailsOnSourceError(t *testing.T) {
	src := sources()
	delete(src.commits, "ogen-app/ui ghcr.io/ogen-app/ui@994...ghcr.io/ogen-app/ui@1001")
	if _, err := Collect(t.Context(), src, changes); err == nil || !strings.Contains(err.Error(), "ui:") {
		t.Fatalf("err = %v", err)
	}
}
