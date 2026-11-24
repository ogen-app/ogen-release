package linear

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve answers each GraphQL request with handler(query, variables).
func serve(t *testing.T, handler func(query string, vars map[string]any) string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "lin_api_key" {
			t.Errorf("Authorization = %q", got)
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(handler(req.Query, req.Variables)))
	}))
	t.Cleanup(srv.Close)
	return &Client{Endpoint: srv.URL, APIKey: "lin_api_key"}
}

func TestIssue(t *testing.T) {
	c := serve(t, func(_ string, vars map[string]any) string {
		if vars["id"] != "CON-517" {
			t.Errorf("id = %v", vars["id"])
		}
		return `{"data":{"issue":{"id":"uuid-1","identifier":"CON-517","title":"Release pipeline 2","url":"https://linear.app/ogen/issue/CON-517",` +
			`"labels":{"nodes":[{"name":"Feature"},{"name":"Infrastructure"}]}}}}`
	})
	is, err := c.Issue(t.Context(), "CON-517")
	if err != nil {
		t.Fatal(err)
	}
	if is.ID != "uuid-1" || is.Title != "Release pipeline 2" || !is.HasLabel("Feature") || is.HasLabel("Bug") {
		t.Errorf("issue = %+v", is)
	}
}

// Labels on a later page still count: they decide the issue's section and
// whether a release already marked it.
func TestIssueReadsEveryLabelPage(t *testing.T) {
	c := serve(t, func(_ string, vars map[string]any) string {
		if vars["after"] == nil {
			return `{"data":{"issue":{"id":"uuid-1","identifier":"CON-1","title":"t","url":"u",` +
				`"labels":{"nodes":[{"name":"Infrastructure"}],"pageInfo":{"hasNextPage":true,"endCursor":"p2"}}}}}`
		}
		if vars["after"] != "p2" {
			t.Errorf("after = %v", vars["after"])
		}
		return `{"data":{"issue":{"labels":{"nodes":[{"name":"Feature"},{"name":"Ogen 2"}],"pageInfo":{"hasNextPage":false}}}}}`
	})
	is, err := c.Issue(t.Context(), "CON-1")
	if err != nil {
		t.Fatal(err)
	}
	if !is.HasLabel("Infrastructure") || !is.HasLabel("Feature") || !is.HasLabel("Ogen 2") {
		t.Errorf("labels = %v", is.Labels)
	}
}

func TestIssueLabelPageError(t *testing.T) {
	c := serve(t, func(_ string, vars map[string]any) string {
		if vars["after"] == nil {
			return `{"data":{"issue":{"id":"uuid-1","identifier":"CON-1","title":"t","url":"u",` +
				`"labels":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"p2"}}}}}`
		}
		return `{"data":null,"errors":[{"message":"rate limited"}]}`
	})
	if _, err := c.Issue(t.Context(), "CON-1"); err == nil || !strings.Contains(err.Error(), "labels") {
		t.Fatalf("err = %v, want a labels error rather than an incomplete issue", err)
	}
}

func TestIssueNotFound(t *testing.T) {
	c := serve(t, func(string, map[string]any) string {
		return `{"data":null,"errors":[{"message":"Entity not found: Issue"}]}`
	})
	if _, err := c.Issue(t.Context(), "CON-9999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// An existing workspace label is reused; a team label of the same name is not.
func TestReleaseLabelReusesWorkspaceLabel(t *testing.T) {
	c := serve(t, func(query string, _ map[string]any) string {
		if strings.Contains(query, "issueLabelCreate") {
			t.Error("created a label that exists")
		}
		return `{"data":{"issueLabels":{"nodes":[{"id":"team-label","team":{"id":"t"}},{"id":"ws-label","team":null}]}}}`
	})
	id, err := c.ReleaseLabel(t.Context(), "Ogen 2")
	if err != nil || id != "ws-label" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
}

// A workspace "Ogen N" that is a group, or in one, can't hold stacking
// releases: refuse it rather than reuse it or create a second one.
func TestReleaseLabelRefusesGroupedLabel(t *testing.T) {
	for name, node := range map[string]string{
		"in a group": `{"id":"l","isGroup":false,"team":null,"parent":{"id":"g"}}`,
		"a group":    `{"id":"l","isGroup":true,"team":null,"parent":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := serve(t, func(query string, _ map[string]any) string {
				if strings.Contains(query, "issueLabelCreate") {
					t.Error("created a second label")
				}
				return `{"data":{"issueLabels":{"nodes":[` + node + `]}}}`
			})
			if _, err := c.ReleaseLabel(t.Context(), "Ogen 2"); err == nil || !strings.Contains(err.Error(), "label group") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestReleaseLabelCreatesIt(t *testing.T) {
	c := serve(t, func(query string, vars map[string]any) string {
		if strings.Contains(query, "issueLabelCreate") {
			if vars["name"] != "Ogen 2" {
				t.Errorf("name = %v", vars["name"])
			}
			return `{"data":{"issueLabelCreate":{"success":true,"issueLabel":{"id":"new-label"}}}}`
		}
		return `{"data":{"issueLabels":{"nodes":[]}}}`
	})
	id, err := c.ReleaseLabel(t.Context(), "Ogen 2")
	if err != nil || id != "new-label" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
}

// The comment goes first and the label last, so a label on an issue means it
// was fully marked.
func TestMarkShippedOrder(t *testing.T) {
	var calls []string
	c := serve(t, func(query string, _ map[string]any) string {
		switch {
		case strings.Contains(query, "commentCreate"):
			calls = append(calls, "comment")
			return `{"data":{"commentCreate":{"success":true}}}`
		case strings.Contains(query, "issueAddLabel"):
			calls = append(calls, "label")
			return `{"data":{"issueAddLabel":{"success":true}}}`
		}
		t.Errorf("unexpected query %q", query)
		return `{}`
	})
	if err := c.MarkShipped(t.Context(), "uuid-1", "label-1", "Shipped in Ogen 2"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "comment,label" {
		t.Errorf("calls = %v", calls)
	}
}

func TestMarkShippedStopsWhenCommentFails(t *testing.T) {
	c := serve(t, func(query string, _ map[string]any) string {
		if strings.Contains(query, "issueAddLabel") {
			t.Error("labelled an issue whose comment failed")
		}
		return `{"data":null,"errors":[{"message":"rate limited"}]}`
	})
	if err := c.MarkShipped(t.Context(), "uuid-1", "label-1", "x"); err == nil {
		t.Fatal("want error")
	}
}
