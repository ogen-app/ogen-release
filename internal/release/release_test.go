package release

import (
	"os"
	"slices"
	"strings"
	"testing"
)

const sample = `# comment
services:   # deploy order
  pdf-service:
    image: ghcr.io/ogen-app/pdf-service
    build: 7
    repo: ogen-app/pdf-service
    railwayServiceId: svc-pdf
  api:
    image: ghcr.io/ogen-app/ogen
    build: 1551 # pinned
    repo: ogen-app/ogen
    railwayServiceId: svc-api
  ui: { image: ghcr.io/ogen-app/ui, build: 99, repo: ogen-app/ui, railwayServiceId: svc-ui }

environments:
  development:
    railwayEnvironmentId: env-dev
`

func TestParse(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, s := range f.Services {
		names = append(names, s.Name)
	}
	if got, want := strings.Join(names, ","), "pdf-service,api,ui"; got != want {
		t.Errorf("order = %s, want %s", got, want)
	}

	api, ok := f.Service("api")
	if !ok {
		t.Fatal("api not found")
	}
	if api.Build != 1551 || !slices.Equal(api.RailwayServiceIDs, []string{"svc-api"}) || api.Repo != "ogen-app/ogen" {
		t.Errorf("api = %+v", api)
	}
	if got, want := api.ImageRef(), "ghcr.io/ogen-app/ogen:1551"; got != want {
		t.Errorf("ImageRef = %s, want %s", got, want)
	}
	if got := f.Environments["development"].RailwayEnvironmentID; got != "env-dev" {
		t.Errorf("development env id = %q", got)
	}
}

func TestParseErrors(t *testing.T) {
	for name, data := range map[string]string{
		"empty":         "",
		"no services":   "environments: {}\n",
		"unknown key":   "services:\n  api: { image: i, build: 1, repo: r, railwayId: x }\n",
		"missing image": "services:\n  api: { build: 1, repo: r }\n",
		"missing build": "services:\n  api: { image: i, repo: r }\n",
		"negative":      "services:\n  api: { image: i, build: -1, repo: r }\n",
		"not a number":  "services:\n  api: { image: i, build: abc, repo: r }\n",
		"both id keys":  "services:\n  api: { image: i, build: 1, repo: r, railwayServiceId: a, railwayServiceIds: [b] }\n",
		"empty id":      "services:\n  api: { image: i, build: 1, repo: r, railwayServiceIds: [a, \"\"] }\n",
		"duplicate id":  "services:\n  api: { image: i, build: 1, repo: r, railwayServiceIds: [a, a] }\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data)); err == nil {
				t.Error("want error")
			}
		})
	}
}

func TestParseRailwayServiceIDs(t *testing.T) {
	for name, tc := range map[string]struct {
		entry string
		want  []string
	}{
		"one id":         {"railwayServiceId: a", []string{"a"}},
		"several ids":    {"railwayServiceIds: [light, heavy]", []string{"light", "heavy"}},
		"empty id":       {`railwayServiceId: ""`, nil},
		"no id":          {"", nil},
		"empty list":     {"railwayServiceIds: []", nil},
		"empty id, list": {`railwayServiceId: "", railwayServiceIds: [a]`, []string{"a"}},
	} {
		t.Run(name, func(t *testing.T) {
			entry := "image: i, build: 1, repo: r"
			if tc.entry != "" {
				entry += ", " + tc.entry
			}
			f, err := Parse([]byte("services:\n  river-service: { " + entry + " }\n"))
			if err != nil {
				t.Fatal(err)
			}
			if got := f.Services[0].RailwayServiceIDs; !slices.Equal(got, tc.want) {
				t.Errorf("RailwayServiceIDs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetBuild(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		service, from, to string
		build             int
	}{
		{"api", "build: 1551 # pinned", "build: 1552 # pinned", 1552},
		{"pdf-service", "build: 7\n", "build: 10\n", 10},
		{"ui", "build: 99,", "build: 100,", 100},
	} {
		t.Run(tc.service, func(t *testing.T) {
			out, err := f.SetBuild(tc.service, tc.build)
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.Replace(sample, tc.from, tc.to, 1); string(out) != want {
				t.Errorf("got:\n%s\nwant:\n%s", out, want)
			}
			g, err := Parse(out)
			if err != nil {
				t.Fatal(err)
			}
			if s, _ := g.Service(tc.service); s.Build != tc.build {
				t.Errorf("reparsed build = %d, want %d", s.Build, tc.build)
			}
		})
	}

	if _, err := f.SetBuild("nope", 1); err == nil {
		t.Error("unknown service: want error")
	}
	if _, err := f.SetBuild("api", -1); err == nil {
		t.Error("negative build: want error")
	}
}

// yaml.v3 reports columns in characters; a non-ASCII prefix on the build's
// line must not shift the edit onto another value.
func TestSetBuildAfterNonASCII(t *testing.T) {
	data := "services:\n  ééééééééééééé: { image: i, repo: ogen-app/12345, build: 100 }\n"
	f, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.SetBuild("ééééééééééééé", 101)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(data, "build: 100", "build: 101", 1); string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// The committed release.yaml must always parse.
func TestRepoReleaseYAML(t *testing.T) {
	data, err := os.ReadFile("../../release.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatal(err)
	}
}
