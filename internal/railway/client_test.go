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

// The shape Railway returns for an image-sourced instance: the legacy
// numReplicas and region are null, and the regions live in the latest
// deployment's manifest.
func TestServiceInstance(t *testing.T) {
	c := serve(t, func(map[string]any) string {
		return `{"data":{"serviceInstance":{"source":{"image":"ghcr.io/ogen-app/ogen:1551"},"numReplicas":null,"region":null,` +
			`"latestDeployment":{"id":"d1","status":"SUCCESS","meta":{"image":"ghcr.io/ogen-app/ogen:1550","reason":"deploy",` +
			`"serviceManifest":{"deploy":{"numReplicas":1,"region":null,"multiRegionConfig":{"europe-west4-drams3a":{"numReplicas":1}}}}}}}}}`
	})
	inst, err := c.ServiceInstance(t.Context(), "svc", "env")
	if err != nil {
		t.Fatal(err)
	}
	if inst.Image != "ghcr.io/ogen-app/ogen:1551" ||
		inst.LatestDeployment.Status != StatusSuccess || inst.LatestDeployment.Image != "ghcr.io/ogen-app/ogen:1550" {
		t.Errorf("instance = %+v", inst)
	}
	if got := string(inst.MultiRegionConfig); got != `{"europe-west4-drams3a":{"numReplicas":1}}` {
		t.Errorf("MultiRegionConfig = %s", got)
	}
}

func TestServiceInstanceLegacyReplicasAndRegion(t *testing.T) {
	c := serve(t, func(map[string]any) string {
		return `{"data":{"serviceInstance":{"source":{"image":"img:1"},"numReplicas":2,"region":"us-west2",` +
			`"latestDeployment":{"id":"d1","status":"SUCCESS","meta":{"image":"img:1"}}}}}`
	})
	inst, err := c.ServiceInstance(t.Context(), "svc", "env")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(inst.MultiRegionConfig); got != `{"us-west2":{"numReplicas":2}}` {
		t.Errorf("MultiRegionConfig = %s", got)
	}
}

func TestServiceInstanceNoRegions(t *testing.T) {
	for name, deploy := range map[string]string{
		"null config":  `{"multiRegionConfig":null}`,
		"empty config": `{"multiRegionConfig":{}}`,
		"no config":    `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := serve(t, func(map[string]any) string {
				return `{"data":{"serviceInstance":{"source":{"image":"img:1"},"numReplicas":null,"region":null,` +
					`"latestDeployment":{"id":"d1","status":"SUCCESS","meta":{"image":"img:1","serviceManifest":{"deploy":` + deploy + `}}}}}}`
			})
			inst, err := c.ServiceInstance(t.Context(), "svc", "env")
			if err != nil {
				t.Fatal(err)
			}
			if inst.MultiRegionConfig != nil {
				t.Errorf("MultiRegionConfig = %s, want nil", inst.MultiRegionConfig)
			}
		})
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
	if inst.Image != "" || inst.MultiRegionConfig != nil || inst.LatestDeployment != nil {
		t.Errorf("instance = %+v", inst)
	}
}

func TestUpdateSourceSendsMultiRegionConfig(t *testing.T) {
	var input map[string]any
	c := serve(t, func(vars map[string]any) string {
		input = vars["input"].(map[string]any)
		return `{"data":{"serviceInstanceUpdate":true}}`
	})
	u := SourceUpdate{ServiceID: "s", EnvironmentID: "e", Image: "img:1",
		MultiRegionConfig: json.RawMessage(`{"us-west2":{"numReplicas":1}}`)}
	if err := c.UpdateSource(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	regions, _ := input["multiRegionConfig"].(map[string]any)
	if r, _ := regions["us-west2"].(map[string]any); r["numReplicas"] != 1.0 || len(regions) != 1 ||
		input["source"].(map[string]any)["image"] != "img:1" {
		t.Errorf("input = %v", input)
	}
	if _, ok := input["numReplicas"]; ok {
		t.Errorf("input still sends the legacy numReplicas: %v", input)
	}
}

func TestUpdateSourceRefusesNoRegions(t *testing.T) {
	c := serve(t, func(map[string]any) string {
		t.Error("UpdateSource called Railway without a multi-region config")
		return `{"data":{"serviceInstanceUpdate":true}}`
	})
	if err := c.UpdateSource(t.Context(), SourceUpdate{ServiceID: "s", EnvironmentID: "e", Image: "img:1"}); err == nil {
		t.Fatal("err = nil")
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
