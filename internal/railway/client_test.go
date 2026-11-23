package railway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, handler func(vars map[string]any) string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Project-Access-Token"); got != "tok" {
			t.Errorf("Project-Access-Token = %q", got)
		}
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(handler(req.Variables)))
	}))
	t.Cleanup(srv.Close)
	return &Client{Endpoint: srv.URL, Token: "tok"}
}

func TestServiceInstance(t *testing.T) {
	c := serve(t, func(map[string]any) string {
		return `{"data":{"serviceInstance":{"source":{"image":"ghcr.io/ogen-app/ogen:1551"},"numReplicas":2,"region":"europe-west4","latestDeployment":{"id":"d1","status":"SUCCESS","meta":{"image":"ghcr.io/ogen-app/ogen:1550","reason":"deploy"}}}}}`
	})
	inst, err := c.ServiceInstance(t.Context(), "svc", "env")
	if err != nil {
		t.Fatal(err)
	}
	if inst.Image != "ghcr.io/ogen-app/ogen:1551" || *inst.NumReplicas != 2 || *inst.Region != "europe-west4" ||
		inst.LatestDeployment.Status != StatusSuccess || inst.LatestDeployment.Image != "ghcr.io/ogen-app/ogen:1550" {
		t.Errorf("instance = %+v", inst)
	}
}

func TestServiceInstanceRepoSource(t *testing.T) {
	c := serve(t, func(map[string]any) string {
		return `{"data":{"serviceInstance":{"source":{"image":null},"numReplicas":null,"region":null,"latestDeployment":null}}}`
	})
	inst, err := c.ServiceInstance(t.Context(), "svc", "env")
	if err != nil {
		t.Fatal(err)
	}
	if inst.Image != "" || inst.NumReplicas != nil || inst.LatestDeployment != nil {
		t.Errorf("instance = %+v", inst)
	}
}

func TestUpdateSourceSendsReplicasAndRegion(t *testing.T) {
	var input map[string]any
	c := serve(t, func(vars map[string]any) string {
		input = vars["input"].(map[string]any)
		return `{"data":{"serviceInstanceUpdate":true}}`
	})
	u := SourceUpdate{ServiceID: "s", EnvironmentID: "e", Image: "img:1", NumReplicas: 1, Region: "us-west2"}
	if err := c.UpdateSource(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if input["numReplicas"] != 1.0 || input["region"] != "us-west2" || input["source"].(map[string]any)["image"] != "img:1" {
		t.Errorf("input = %v", input)
	}
}

func TestGraphQLErrors(t *testing.T) {
	c := serve(t, func(map[string]any) string {
		return `{"data":null,"errors":[{"message":"Not Authorized"},{"message":"other"}]}`
	})
	_, err := c.Deploy(t.Context(), "s", "e")
	if err == nil || !strings.Contains(err.Error(), "Not Authorized; other") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeployReturnsID(t *testing.T) {
	c := serve(t, func(map[string]any) string {
		return `{"data":{"serviceInstanceDeployV2":"dep-1"}}`
	})
	id, err := c.Deploy(t.Context(), "s", "e")
	if err != nil || id != "dep-1" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
}
