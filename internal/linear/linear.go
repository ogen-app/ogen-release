// Package linear is the part of Linear's GraphQL API the release uses: it reads
// the issues a release ships and marks them shipped.
//
// Linear Releases need the Business plan, so a release is recorded with a
// workspace label "Ogen N" on each issue and a comment linking the GitHub
// Release. The labels are not in a label group: Linear allows one label per
// group, and an issue that ships in two releases keeps both.
package linear

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

// Endpoint is Linear's GraphQL API.
const Endpoint = "https://api.linear.app/graphql"

// releaseColor is the colour of the "Ogen N" labels.
const releaseColor = "#0F6C66"

// Issue is what a release note shows about an issue.
type Issue struct {
	ID         string // Linear's id, for mutations
	Identifier string // e.g. CON-517
	Title      string
	URL        string
	Labels     []string
}

// HasLabel reports whether the issue carries the named label.
func (i Issue) HasLabel(name string) bool {
	return slices.Contains(i.Labels, name)
}

// ErrNotFound is returned for an identifier Linear doesn't know.
var ErrNotFound = errors.New("issue not found")

// Client calls Linear's API with a personal API key.
type Client struct {
	Endpoint   string // defaults to Endpoint
	APIKey     string
	HTTPClient *http.Client
}

// labelPage is one page of an issue's labels.
type labelPage struct {
	Nodes []struct {
		Name string `json:"name"`
	} `json:"nodes"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

// Issue returns the issue with the given identifier, with all its labels: the
// section it goes in and whether a release already marked it depend on them.
func (c *Client) Issue(ctx context.Context, identifier string) (Issue, error) {
	const q = `query($id: String!) {
  issue(id: $id) {
    id identifier title url
    labels(first: 100) { nodes { name } pageInfo { hasNextPage endCursor } }
  }
}`
	var out struct {
		Issue *struct {
			ID         string    `json:"id"`
			Identifier string    `json:"identifier"`
			Title      string    `json:"title"`
			URL        string    `json:"url"`
			Labels     labelPage `json:"labels"`
		} `json:"issue"`
	}
	if err := c.do(ctx, q, map[string]any{"id": identifier}, &out); err != nil {
		if strings.Contains(err.Error(), "Entity not found") {
			return Issue{}, fmt.Errorf("%s: %w", identifier, ErrNotFound)
		}
		return Issue{}, fmt.Errorf("%s: %w", identifier, err)
	}
	if out.Issue == nil {
		return Issue{}, fmt.Errorf("%s: %w", identifier, ErrNotFound)
	}
	is := Issue{ID: out.Issue.ID, Identifier: out.Issue.Identifier, Title: out.Issue.Title, URL: out.Issue.URL}

	const more = `query($id: String!, $after: String!) {
  issue(id: $id) { labels(first: 100, after: $after) { nodes { name } pageInfo { hasNextPage endCursor } } }
}`
	page := out.Issue.Labels
	for {
		for _, l := range page.Nodes {
			is.Labels = append(is.Labels, l.Name)
		}
		if !page.PageInfo.HasNextPage {
			return is, nil
		}
		var next struct {
			Issue struct {
				Labels labelPage `json:"labels"`
			} `json:"issue"`
		}
		vars := map[string]any{"id": identifier, "after": page.PageInfo.EndCursor}
		if err := c.do(ctx, more, vars, &next); err != nil {
			return Issue{}, fmt.Errorf("%s: labels: %w", identifier, err)
		}
		page = next.Issue.Labels
	}
}

// ReleaseLabel returns the id of the workspace label name, creating it if it
// doesn't exist. Only an ungrouped label will do: a group allows one of its
// labels per issue, so an issue shipped in two releases would lose one. A
// workspace label of that name that is a group or in one is an error.
func (c *Client) ReleaseLabel(ctx context.Context, name string) (string, error) {
	const find = `query($name: String!) {
  issueLabels(first: 50, filter: { name: { eq: $name } }) { nodes { id isGroup team { id } parent { id } } }
}`
	var found struct {
		IssueLabels struct {
			Nodes []struct {
				ID      string `json:"id"`
				IsGroup bool   `json:"isGroup"`
				Team    *struct {
					ID string `json:"id"`
				} `json:"team"`
				Parent *struct {
					ID string `json:"id"`
				} `json:"parent"`
			} `json:"nodes"`
		} `json:"issueLabels"`
	}
	if err := c.do(ctx, find, map[string]any{"name": name}, &found); err != nil {
		return "", fmt.Errorf("find label %s: %w", name, err)
	}
	for _, l := range found.IssueLabels.Nodes {
		if l.Team != nil {
			continue // a team's own label of the same name isn't the release label
		}
		if l.IsGroup || l.Parent != nil {
			return "", fmt.Errorf("the workspace label %q is a label group or inside one; Linear allows one label "+
				"per group on an issue, so releases couldn't stack. Move it out of the group (or rename it) and re-run", name)
		}
		return l.ID, nil
	}

	const create = `mutation($name: String!, $color: String!) {
  issueLabelCreate(input: { name: $name, color: $color }) { success issueLabel { id } }
}`
	var created struct {
		IssueLabelCreate struct {
			Success    bool `json:"success"`
			IssueLabel struct {
				ID string `json:"id"`
			} `json:"issueLabel"`
		} `json:"issueLabelCreate"`
	}
	if err := c.do(ctx, create, map[string]any{"name": name, "color": releaseColor}, &created); err != nil {
		return "", fmt.Errorf("create label %s: %w", name, err)
	}
	if !created.IssueLabelCreate.Success || created.IssueLabelCreate.IssueLabel.ID == "" {
		return "", fmt.Errorf("create label %s: not created", name)
	}
	return created.IssueLabelCreate.IssueLabel.ID, nil
}

// MarkShipped comments on the issue and adds the release label. The label is
// added last, so an issue that already has it was fully marked before; the
// caller skips it, which makes re-running a release safe.
func (c *Client) MarkShipped(ctx context.Context, issueID, labelID, comment string) error {
	const commentQ = `mutation($id: String!, $body: String!) {
  commentCreate(input: { issueId: $id, body: $body }) { success }
}`
	var com struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	if err := c.do(ctx, commentQ, map[string]any{"id": issueID, "body": comment}, &com); err != nil {
		return fmt.Errorf("comment: %w", err)
	}
	if !com.CommentCreate.Success {
		return errors.New("comment: not created")
	}

	const labelQ = `mutation($id: String!, $label: String!) {
  issueAddLabel(id: $id, labelId: $label) { success }
}`
	var lab struct {
		IssueAddLabel struct {
			Success bool `json:"success"`
		} `json:"issueAddLabel"`
	}
	if err := c.do(ctx, labelQ, map[string]any{"id": issueID, "label": labelID}, &lab); err != nil {
		return fmt.Errorf("add label: %w", err)
	}
	if !lab.IssueAddLabel.Success {
		return errors.New("add label: not added")
	}
	return nil
}

func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cmp.Or(c.Endpoint, Endpoint), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.APIKey) // a personal API key goes in as is, without "Bearer"

	resp, err := cmp.Or(c.HTTPClient, http.DefaultClient).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("linear: HTTP %d", resp.StatusCode)
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, len(res.Errors))
		for i, e := range res.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("linear: %s", strings.Join(msgs, "; "))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("linear: HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(res.Data, out)
}
