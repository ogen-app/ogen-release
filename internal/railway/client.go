// Package railway is a small client for the parts of Railway's public GraphQL
// API (https://backboard.railway.com/graphql/v2) that deploy an image to a
// service instance.
//
// It authenticates with a project token, which is scoped to one environment of
// one project.
package railway

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Endpoint is Railway's public GraphQL API.
const Endpoint = "https://backboard.railway.com/graphql/v2"

// Deployment statuses this package acts on. Railway has more (BUILDING,
// DEPLOYING, QUEUED, …); anything not listed here is still in progress.
const (
	StatusSuccess  = "SUCCESS"
	StatusSleeping = "SLEEPING"
	StatusFailed   = "FAILED"
	StatusCrashed  = "CRASHED"
	StatusRemoved  = "REMOVED"
	StatusSkipped  = "SKIPPED"
)

// Client calls Railway's GraphQL API with a project token.
type Client struct {
	Endpoint   string
	Token      string
	HTTPClient *http.Client
}

// ProjectToken is what a project token is scoped to.
type ProjectToken struct {
	ProjectID     string
	EnvironmentID string
}

// Deployment is one deployment of a service instance.
type Deployment struct {
	ID     string
	Status string
	Image  string // the image it runs, from its meta; empty if Railway didn't say
}

// ServiceInstance is a service's configuration in one environment.
type ServiceInstance struct {
	Image            string // source image with tag; empty for a repo source
	NumReplicas      *int
	Region           *string
	LatestDeployment *Deployment
}

// SourceUpdate changes a service instance's source image. Replicas and region
// are always sent with it because Railway is reported to reset them otherwise.
type SourceUpdate struct {
	ServiceID     string
	EnvironmentID string
	Image         string
	NumReplicas   int
	Region        string
}

// Variable is a service variable to set.
type Variable struct {
	ProjectID     string
	EnvironmentID string
	ServiceID     string
	Name          string
	Value         string
}

// ProjectToken returns the project and environment the token is scoped to.
func (c *Client) ProjectToken(ctx context.Context) (ProjectToken, error) {
	var out struct {
		ProjectToken struct {
			ProjectID     string `json:"projectId"`
			EnvironmentID string `json:"environmentId"`
		} `json:"projectToken"`
	}
	if err := c.do(ctx, `query { projectToken { projectId environmentId } }`, nil, &out); err != nil {
		return ProjectToken{}, err
	}
	return ProjectToken(out.ProjectToken), nil
}

// ServiceInstance returns a service's configuration in an environment.
func (c *Client) ServiceInstance(ctx context.Context, serviceID, environmentID string) (ServiceInstance, error) {
	const q = `query($serviceId: String!, $environmentId: String!) {
  serviceInstance(serviceId: $serviceId, environmentId: $environmentId) {
    source { image }
    numReplicas
    region
    latestDeployment { id status meta }
  }
}`
	var out struct {
		ServiceInstance struct {
			Source *struct {
				Image *string `json:"image"`
			} `json:"source"`
			NumReplicas      *int    `json:"numReplicas"`
			Region           *string `json:"region"`
			LatestDeployment *struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Meta   *struct {
					Image string `json:"image"`
				} `json:"meta"`
			} `json:"latestDeployment"`
		} `json:"serviceInstance"`
	}
	vars := map[string]any{"serviceId": serviceID, "environmentId": environmentID}
	if err := c.do(ctx, q, vars, &out); err != nil {
		return ServiceInstance{}, err
	}

	si := out.ServiceInstance
	inst := ServiceInstance{NumReplicas: si.NumReplicas, Region: si.Region}
	if si.Source != nil && si.Source.Image != nil {
		inst.Image = *si.Source.Image
	}
	if d := si.LatestDeployment; d != nil {
		inst.LatestDeployment = &Deployment{ID: d.ID, Status: d.Status}
		if d.Meta != nil {
			inst.LatestDeployment.Image = d.Meta.Image
		}
	}
	return inst, nil
}

// UpdateSource points a service instance at an image. It doesn't deploy.
func (c *Client) UpdateSource(ctx context.Context, u SourceUpdate) error {
	const q = `mutation($serviceId: String!, $environmentId: String!, $input: ServiceInstanceUpdateInput!) {
  serviceInstanceUpdate(serviceId: $serviceId, environmentId: $environmentId, input: $input)
}`
	type source struct {
		Image string `json:"image"`
	}
	type input struct {
		Source      source `json:"source"`
		NumReplicas int    `json:"numReplicas"`
		Region      string `json:"region"`
	}
	vars := map[string]any{
		"serviceId":     u.ServiceID,
		"environmentId": u.EnvironmentID,
		"input":         input{Source: source{Image: u.Image}, NumReplicas: u.NumReplicas, Region: u.Region},
	}
	return c.do(ctx, q, vars, nil)
}

// UpsertVariable sets a service variable without triggering a deploy.
func (c *Client) UpsertVariable(ctx context.Context, v Variable) error {
	const q = `mutation($input: VariableUpsertInput!) { variableUpsert(input: $input) }`
	vars := map[string]any{"input": map[string]any{
		"projectId":     v.ProjectID,
		"environmentId": v.EnvironmentID,
		"serviceId":     v.ServiceID,
		"name":          v.Name,
		"value":         v.Value,
		"skipDeploys":   true,
	}}
	return c.do(ctx, q, vars, nil)
}

// Deploy starts a deployment of a service instance's current configuration and
// returns its id.
func (c *Client) Deploy(ctx context.Context, serviceID, environmentID string) (string, error) {
	const q = `mutation($serviceId: String!, $environmentId: String!) {
  serviceInstanceDeployV2(serviceId: $serviceId, environmentId: $environmentId)
}`
	var out struct {
		ID string `json:"serviceInstanceDeployV2"`
	}
	vars := map[string]any{"serviceId": serviceID, "environmentId": environmentID}
	if err := c.do(ctx, q, vars, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("serviceInstanceDeployV2 returned no deployment id")
	}
	return out.ID, nil
}

// DeploymentStatus returns a deployment's status.
func (c *Client) DeploymentStatus(ctx context.Context, id string) (string, error) {
	const q = `query($id: String!) { deployment(id: $id) { status } }`
	var out struct {
		Deployment struct {
			Status string `json:"status"`
		} `json:"deployment"`
	}
	if err := c.do(ctx, q, map[string]any{"id": id}, &out); err != nil {
		return "", err
	}
	return out.Deployment.Status, nil
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
	req.Header.Set("Project-Access-Token", c.Token)

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
		return fmt.Errorf("railway: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, len(res.Errors))
		for i, e := range res.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("railway: %s", strings.Join(msgs, "; "))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("railway: HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res.Data, out)
}
