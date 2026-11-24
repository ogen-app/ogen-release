package ghcr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// registry serves one image the way ghcr.io does: an anonymous token, an OCI
// index pointing at a linux manifest, and a config blob with labels.
func registry(t *testing.T, labels string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" && r.Header.Get("Authorization") != "Bearer anon" {
			t.Errorf("%s: Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/token":
			if got := r.URL.Query().Get("scope"); got != "repository:ogen-app/ogen:pull" {
				t.Errorf("scope = %q", got)
			}
			w.Write([]byte(`{"token":"anon"}`))
		case "/v2/ogen-app/ogen/manifests/1552":
			w.Write([]byte(`{"manifests":[{"digest":"sha256:attest","platform":{"os":"unknown"}},` +
				`{"digest":"sha256:linux","platform":{"os":"linux","architecture":"amd64"}}]}`))
		case "/v2/ogen-app/ogen/manifests/sha256:linux":
			w.Write([]byte(`{"config":{"digest":"sha256:cfg"}}`))
		case "/v2/ogen-app/ogen/blobs/sha256:cfg":
			w.Write([]byte(`{"config":{"Labels":` + labels + `}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}
}

func TestRevision(t *testing.T) {
	c := registry(t, `{"org.opencontainers.image.revision":"757138981b5ddf9bff1ad65e2a61441301b4c0f0"}`)
	rev, err := c.Revision(t.Context(), "ghcr.io/ogen-app/ogen", "1552")
	if err != nil {
		t.Fatal(err)
	}
	if rev != "757138981b5ddf9bff1ad65e2a61441301b4c0f0" {
		t.Errorf("revision = %q", rev)
	}
}

func TestRevisionErrors(t *testing.T) {
	c := registry(t, `{}`)
	for name, tc := range map[string]struct{ image, tag, want string }{
		"no label":    {"ghcr.io/ogen-app/ogen", "1552", "no org.opencontainers.image.revision label"},
		"missing tag": {"ghcr.io/ogen-app/ogen", "9999", "HTTP 404"},
		"not ghcr":    {"docker.io/ogen-app/ogen", "1", "not a ghcr.io image"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.Revision(t.Context(), tc.image, tc.tag)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
