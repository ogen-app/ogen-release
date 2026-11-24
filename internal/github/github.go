// Package github lists the commits between two revisions of a service repo.
package github

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// API is GitHub's REST API.
const API = "https://api.github.com"

// Commit is one commit, with only the first line of its message.
type Commit struct {
	SHA     string
	Subject string
	URL     string
}

// Client calls GitHub's REST API. Token is optional for public repos; with one
// the rate limit is much higher.
type Client struct {
	BaseURL    string // defaults to API
	Token      string
	HTTPClient *http.Client
}

// maxCommits is the most commits the compare API returns. A release between
// builds further apart than that is reported as an error instead of silently
// missing commits.
const maxCommits = 250

// Commits returns the commits in repo after base up to and including head,
// oldest first.
func (c *Client) Commits(ctx context.Context, repo, base, head string) ([]Commit, error) {
	url := fmt.Sprintf("%s/repos/%s/compare/%s...%s", cmp.Or(c.BaseURL, API), repo, base, head)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := cmp.Or(c.HTTPClient, http.DefaultClient).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("compare %s %s...%s: HTTP %d", repo, short(base), short(head), resp.StatusCode)
	}

	var out struct {
		TotalCommits int `json:"total_commits"`
		Commits      []struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
			Commit  struct {
				Message string `json:"message"`
			} `json:"commit"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("compare %s: %w", repo, err)
	}
	if out.TotalCommits > maxCommits || out.TotalCommits > len(out.Commits) {
		return nil, fmt.Errorf("compare %s %s...%s: %d commits, more than the %d the API lists",
			repo, short(base), short(head), out.TotalCommits, maxCommits)
	}
	commits := make([]Commit, len(out.Commits))
	for i, cm := range out.Commits {
		subject, _, _ := strings.Cut(cm.Commit.Message, "\n")
		commits[i] = Commit{SHA: cm.SHA, Subject: strings.TrimSpace(subject), URL: cm.HTMLURL}
	}
	return commits, nil
}

func short(sha string) string {
	return sha[:min(7, len(sha))]
}
