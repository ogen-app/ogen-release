package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ogen-app/ogen-release/internal/railway"
	"github.com/ogen-app/ogen-release/internal/release"
)

const sample = `services:
  pdf-service: { image: ghcr.io/ogen-app/pdf-service, build: 7, repo: ogen-app/pdf-service, railwayServiceId: svc-pdf }
  api: { image: ghcr.io/ogen-app/ogen, build: 1552, repo: ogen-app/ogen, railwayServiceId: svc-api }
  ui: { image: ghcr.io/ogen-app/ui, build: 99, repo: ogen-app/ui, railwayServiceId: svc-ui }
environments:
  development: { railwayEnvironmentId: env-dev }
  production: { railwayEnvironmentId: env-prod }
`

// fakeRailway records calls and plays back statuses per deployment.
type fakeRailway struct {
	token     railway.ProjectToken
	instances map[string]railway.ServiceInstance
	statuses  map[string][]string // serviceID → statuses returned in turn
	calls     []string
	updates   []railway.SourceUpdate
	vars      []railway.Variable
}

// instance is a service instance running image, with 1 replica in us-west2.
func instance(image string) railway.ServiceInstance {
	return railway.ServiceInstance{
		Image:             image,
		LatestDeployment:  &railway.Deployment{ID: "old", Status: railway.StatusSuccess, Image: image},
		MultiRegionConfig: json.RawMessage(`{"us-west2":{"numReplicas":1}}`),
	}
}

func newFake() *fakeRailway {
	api := instance("ghcr.io/ogen-app/ogen:1551")
	api.MultiRegionConfig = json.RawMessage(`{"europe-west4-drams3a":{"numReplicas":2}}`)
	return &fakeRailway{
		token: railway.ProjectToken{ProjectID: "proj", EnvironmentID: "env-dev"},
		instances: map[string]railway.ServiceInstance{
			"svc-pdf": instance("ghcr.io/ogen-app/pdf-service:7"),
			"svc-api": api,
			"svc-ui":  instance("ghcr.io/ogen-app/ui:99"),
		},
		statuses: map[string][]string{},
	}
}

func (f *fakeRailway) ProjectToken(context.Context) (railway.ProjectToken, error) {
	return f.token, nil
}

func (f *fakeRailway) ServiceInstance(_ context.Context, serviceID, _ string) (railway.ServiceInstance, error) {
	f.calls = append(f.calls, "read "+serviceID)
	return f.instances[serviceID], nil
}

func (f *fakeRailway) UpdateSource(_ context.Context, u railway.SourceUpdate) error {
	f.calls = append(f.calls, "update "+u.ServiceID)
	f.updates = append(f.updates, u)
	return nil
}

func (f *fakeRailway) UpsertVariable(_ context.Context, v railway.Variable) error {
	f.calls = append(f.calls, "var "+v.ServiceID)
	f.vars = append(f.vars, v)
	return nil
}

func (f *fakeRailway) Deploy(_ context.Context, serviceID, _ string) (string, error) {
	f.calls = append(f.calls, "deploy "+serviceID)
	return "dep-" + serviceID, nil
}

func (f *fakeRailway) DeploymentStatus(_ context.Context, id string) (string, error) {
	svc := strings.TrimPrefix(id, "dep-")
	s := f.statuses[svc]
	if len(s) == 0 {
		return railway.StatusSuccess, nil
	}
	f.statuses[svc] = s[1:]
	if s[0] == "error" {
		return "", fmt.Errorf("transient")
	}
	return s[0], nil
}

func parse(t *testing.T) *release.File {
	t.Helper()
	f, err := release.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func opts(env string) options {
	return options{env: env, pollInterval: time.Millisecond, timeout: time.Second, logf: func(string, ...any) {}}
}

func TestDeployOnlyChangedServices(t *testing.T) {
	api := newFake()
	if err := deploy(t.Context(), api, parse(t), opts("development")); err != nil {
		t.Fatal(err)
	}

	want := []string{"read svc-pdf", "read svc-api", "var svc-api", "update svc-api", "deploy svc-api", "read svc-ui"}
	if !slices.Equal(api.calls, want) {
		t.Errorf("calls = %v, want %v", api.calls, want)
	}
	u := api.updates[0]
	if u.Image != "ghcr.io/ogen-app/ogen:1552" || u.EnvironmentID != "env-dev" {
		t.Errorf("update = %+v", u)
	}
	if string(u.MultiRegionConfig) != `{"europe-west4-drams3a":{"numReplicas":2}}` {
		t.Errorf("multi-region config not passed through: %s", u.MultiRegionConfig)
	}
	if v := api.vars[0]; v.Name != "SENTRY_RELEASE" || v.Value != "api@1552" || v.ProjectID != "proj" {
		t.Errorf("variable = %+v", v)
	}
}

func TestDeployRedeploysUnhealthyService(t *testing.T) {
	api := newFake()
	inst := api.instances["svc-pdf"]
	inst.LatestDeployment = &railway.Deployment{ID: "x", Status: railway.StatusCrashed}
	api.instances["svc-pdf"] = inst

	if err := deploy(t.Context(), api, parse(t), opts("development")); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(api.calls, "deploy svc-pdf") {
		t.Errorf("crashed service not redeployed: %v", api.calls)
	}
}

// A source update whose deploy never ran leaves the configured image equal to
// the pin while the old build still runs; so does a manual rollback. Both must
// redeploy, as must a deployment whose image Railway doesn't report.
func TestDeployComparesTheRunningImage(t *testing.T) {
	for name, running := range map[string]string{
		"older build running": "ghcr.io/ogen-app/pdf-service:6",
		"image unknown":       "",
	} {
		t.Run(name, func(t *testing.T) {
			api := newFake()
			inst := api.instances["svc-pdf"]
			inst.LatestDeployment.Image = running
			api.instances["svc-pdf"] = inst

			if err := deploy(t.Context(), api, parse(t), opts("development")); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(api.calls, "deploy svc-pdf") {
				t.Errorf("svc-pdf not redeployed: %v", api.calls)
			}
		})
	}
}

func TestDeployRefusesUnknownReplicasOrRegion(t *testing.T) {
	api := newFake()
	inst := api.instances["svc-api"]
	inst.MultiRegionConfig = nil
	api.instances["svc-api"] = inst

	err := deploy(t.Context(), api, parse(t), opts("development"))
	if err == nil || !strings.Contains(err.Error(), "no replicas or region") {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(api.calls, "update svc-api") {
		t.Error("updated the instance anyway")
	}
}

func TestDeployStopsOnFailure(t *testing.T) {
	for _, status := range []string{railway.StatusFailed, railway.StatusCrashed} {
		t.Run(status, func(t *testing.T) {
			api := newFake()
			inst := api.instances["svc-ui"]
			inst.Image = "ghcr.io/ogen-app/ui:98"
			api.instances["svc-ui"] = inst
			api.statuses["svc-api"] = []string{"DEPLOYING", status}

			err := deploy(t.Context(), api, parse(t), opts("development"))
			if err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("err = %v, want %s", err, status)
			}
			if slices.Contains(api.calls, "read svc-ui") {
				t.Errorf("chain continued after failure: %v", api.calls)
			}
		})
	}
}

func TestDeployTimesOut(t *testing.T) {
	api := newFake()
	api.statuses["svc-api"] = slices.Repeat([]string{"DEPLOYING"}, 1000)
	o := opts("development")
	o.timeout = 20 * time.Millisecond

	err := deploy(t.Context(), api, parse(t), o)
	if err == nil || !strings.Contains(err.Error(), "not done after") {
		t.Fatalf("err = %v, want timeout", err)
	}
}

func TestDeployToleratesTransientPollErrors(t *testing.T) {
	api := newFake()
	api.statuses["svc-api"] = []string{"error", "error", "DEPLOYING", railway.StatusSuccess}
	if err := deploy(t.Context(), api, parse(t), opts("development")); err != nil {
		t.Fatal(err)
	}

	api = newFake()
	api.statuses["svc-api"] = slices.Repeat([]string{"error"}, maxPollErrors)
	if err := deploy(t.Context(), api, parse(t), opts("development")); err == nil {
		t.Fatal("want error after repeated poll failures")
	}
}

func TestDeployRefusesTokenForAnotherEnvironment(t *testing.T) {
	api := newFake()
	err := deploy(t.Context(), api, parse(t), opts("production"))
	if err == nil || !strings.Contains(err.Error(), "scoped to environment env-dev") {
		t.Fatalf("err = %v", err)
	}
	if len(api.calls) != 0 {
		t.Errorf("calls = %v, want none", api.calls)
	}
}

func TestDeployDryRun(t *testing.T) {
	api := newFake()
	o := opts("development")
	o.dryRun = true
	if err := deploy(t.Context(), api, parse(t), o); err != nil {
		t.Fatal(err)
	}
	for _, c := range api.calls {
		if !strings.HasPrefix(c, "read ") {
			t.Errorf("dry run made call %q", c)
		}
	}
}
