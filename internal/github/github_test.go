package github

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/ogen-app/ogen/compare/aaa...bbb" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: "tok"}
}

func TestCommits(t *testing.T) {
	c := serve(t, `{"total_commits":2,"commits":[`+
		`{"sha":"111","html_url":"https://github.com/ogen-app/ogen/commit/111","commit":{"message":"CON-1: first\n\nbody"}},`+
		`{"sha":"222","html_url":"https://github.com/ogen-app/ogen/commit/222","commit":{"message":"Tidy README"}}]}`)
	got, err := c.Commits(t.Context(), "ogen-app/ogen", "aaa", "bbb")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Subject != "CON-1: first" || got[1].Subject != "Tidy README" ||
		got[0].URL != "https://github.com/ogen-app/ogen/commit/111" {
		t.Errorf("commits = %+v", got)
	}
}

// More commits than the API lists must fail, not drop the rest silently.
func TestCommitsTooMany(t *testing.T) {
	c := serve(t, `{"total_commits":300,"commits":[{"sha":"1","commit":{"message":"x"}}]}`)
	if _, err := c.Commits(t.Context(), "ogen-app/ogen", "aaa", "bbb"); err == nil || !strings.Contains(err.Error(), "300 commits") {
		t.Fatalf("err = %v", err)
	}
}

func TestCommitsHTTPError(t *testing.T) {
	c := serve(t, `{}`)
	if _, err := c.Commits(t.Context(), "ogen-app/ogen", "aaa", "zzz"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v", err)
	}
}
