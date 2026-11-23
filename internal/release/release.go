// Package release reads and edits release.yaml, the product lock file that
// pins the build of every service.
package release

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Service is one service pinned in release.yaml.
type Service struct {
	Name  string
	Image string // repository without a tag, e.g. ghcr.io/ogen-app/ogen
	Build int
	Repo  string // GitHub owner/name

	// RailwayServiceIDs are the Railway services that run this image, in
	// deploy order. Most entries have one (railwayServiceId); river-service
	// runs one image as two (railwayServiceIds), and one bump moves both.
	RailwayServiceIDs []string
}

// ImageRef is the image with its build tag, the source a Railway service
// instance runs.
func (s Service) ImageRef() string {
	return s.Image + ":" + strconv.Itoa(s.Build)
}

// Environment is one Railway environment the product deploys to.
type Environment struct {
	Name                 string
	RailwayEnvironmentID string
}

// File is a parsed release.yaml. It keeps the original bytes so SetBuild can
// change one value without reformatting the rest of the file.
type File struct {
	Services     []Service // in file order, which is deploy order
	Environments map[string]Environment

	data []byte
	// buildPos holds where each service's build scalar sits in data.
	buildPos map[string]position
}

type position struct{ line, column int } // 1-based, as yaml.Node reports them

type rawFile struct {
	Services     map[string]rawService     `yaml:"services"`
	Environments map[string]rawEnvironment `yaml:"environments"`
}

type rawService struct {
	Image             string   `yaml:"image"`
	Build             int      `yaml:"build"`
	Repo              string   `yaml:"repo"`
	RailwayServiceID  string   `yaml:"railwayServiceId"`
	RailwayServiceIDs []string `yaml:"railwayServiceIds"`
}

// railwayServiceIDs returns the service's Railway ids from whichever of the
// two keys is set. An empty railwayServiceId is allowed (it's how a service is
// listed before onboarding) and gives no ids.
func (rs rawService) railwayServiceIDs(name string) ([]string, error) {
	if rs.RailwayServiceID != "" && len(rs.RailwayServiceIDs) > 0 {
		return nil, fmt.Errorf("release.yaml: service %s: set railwayServiceId or railwayServiceIds, not both", name)
	}
	if len(rs.RailwayServiceIDs) == 0 {
		if rs.RailwayServiceID == "" {
			return nil, nil
		}
		return []string{rs.RailwayServiceID}, nil
	}
	for i, id := range rs.RailwayServiceIDs {
		if id == "" {
			return nil, fmt.Errorf("release.yaml: service %s: railwayServiceIds has an empty id", name)
		}
		if slices.Contains(rs.RailwayServiceIDs[:i], id) {
			return nil, fmt.Errorf("release.yaml: service %s: railwayServiceIds lists %s twice", name, id)
		}
	}
	return slices.Clone(rs.RailwayServiceIDs), nil
}

type rawEnvironment struct {
	RailwayEnvironmentID string `yaml:"railwayEnvironmentId"`
}

// Parse reads release.yaml. Unknown keys are an error, so a typo can't silently
// drop a setting.
func Parse(data []byte) (*File, error) {
	var raw rawFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode release.yaml: %w", err)
	}

	// The struct decode validates; the node tree gives the service order and
	// where each build value sits.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decode release.yaml: %w", err)
	}
	if len(root.Content) == 0 {
		return nil, errors.New("release.yaml: empty")
	}
	services := mappingValue(root.Content[0], "services")
	if services == nil || len(services.Content) == 0 {
		return nil, errors.New("release.yaml: no services")
	}

	f := &File{
		Environments: make(map[string]Environment, len(raw.Environments)),
		data:         data,
		buildPos:     make(map[string]position, len(raw.Services)),
	}
	for i := 0; i < len(services.Content); i += 2 {
		name := services.Content[i].Value
		rs := raw.Services[name]
		if rs.Image == "" || rs.Repo == "" {
			return nil, fmt.Errorf("release.yaml: service %s: image and repo are required", name)
		}
		if rs.Build < 0 {
			return nil, fmt.Errorf("release.yaml: service %s: negative build %d", name, rs.Build)
		}
		build := mappingValue(services.Content[i+1], "build")
		if build == nil {
			return nil, fmt.Errorf("release.yaml: service %s: build is required", name)
		}
		ids, err := rs.railwayServiceIDs(name)
		if err != nil {
			return nil, err
		}
		f.buildPos[name] = position{build.Line, build.Column}
		f.Services = append(f.Services, Service{
			Name:              name,
			Image:             rs.Image,
			Build:             rs.Build,
			Repo:              rs.Repo,
			RailwayServiceIDs: ids,
		})
	}
	for name, re := range raw.Environments {
		f.Environments[name] = Environment{Name: name, RailwayEnvironmentID: re.RailwayEnvironmentID}
	}
	return f, nil
}

// Service returns the named service.
func (f *File) Service(name string) (Service, bool) {
	for _, s := range f.Services {
		if s.Name == name {
			return s, true
		}
	}
	return Service{}, false
}

// SetBuild returns release.yaml with the named service's build replaced. Only
// that one value changes, so comments and formatting survive and a bump commit
// is a one-line diff.
func (f *File) SetBuild(name string, build int) ([]byte, error) {
	pos, ok := f.buildPos[name]
	if !ok {
		return nil, fmt.Errorf("unknown service %q", name)
	}
	if build < 0 {
		return nil, fmt.Errorf("negative build %d", build)
	}

	start := offset(f.data, pos)
	end := start
	for end < len(f.data) && isScalarByte(f.data[end]) {
		end++
	}
	if _, err := strconv.Atoi(string(f.data[start:end])); err != nil {
		return nil, fmt.Errorf("service %s: build is not a plain integer in release.yaml", name)
	}

	out := make([]byte, 0, len(f.data)+8)
	out = append(out, f.data[:start]...)
	out = strconv.AppendInt(out, int64(build), 10)
	out = append(out, f.data[end:]...)
	return out, nil
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// offset converts a 1-based line and column to a byte offset in data. yaml.v3
// counts columns in characters, not bytes, so the column is walked as UTF-8.
func offset(data []byte, pos position) int {
	off := 0
	for range pos.line - 1 {
		i := bytes.IndexByte(data[off:], '\n')
		if i < 0 {
			return len(data)
		}
		off += i + 1
	}
	for range pos.column - 1 {
		if off >= len(data) {
			break
		}
		_, size := utf8.DecodeRune(data[off:])
		off += size
	}
	return off
}

func isScalarByte(b byte) bool {
	return b != ' ' && b != '\t' && b != '\n' && b != '\r' && b != ',' && b != '}' && b != '#'
}
