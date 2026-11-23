package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ogen-app/ogen-release/internal/railway"
	"github.com/ogen-app/ogen-release/internal/release"
)

// railwayAPI is the part of *railway.Client the deploy uses.
type railwayAPI interface {
	ProjectToken(ctx context.Context) (railway.ProjectToken, error)
	ServiceInstance(ctx context.Context, serviceID, environmentID string) (railway.ServiceInstance, error)
	UpdateSource(ctx context.Context, u railway.SourceUpdate) error
	UpsertVariable(ctx context.Context, v railway.Variable) error
	Deploy(ctx context.Context, serviceID, environmentID string) (string, error)
	DeploymentStatus(ctx context.Context, id string) (string, error)
}

type options struct {
	env          string
	dryRun       bool
	pollInterval time.Duration
	timeout      time.Duration // per service
	logf         func(format string, args ...any)
}

// maxPollErrors is how many status polls in a row may fail before the deploy
// gives up on a service.
const maxPollErrors = 5

// deploy brings every service in env to the build release.yaml pins, in file
// order. Railway is the truth for what runs (D7): a service is deployed when
// its instance's image differs from release.yaml, or its latest deployment
// isn't healthy. The first failure stops the chain.
func deploy(ctx context.Context, api railwayAPI, f *release.File, opts options) error {
	env, ok := f.Environments[opts.env]
	if !ok || env.RailwayEnvironmentID == "" {
		return fmt.Errorf("release.yaml has no railwayEnvironmentId for %s", opts.env)
	}

	// A token scoped to another environment would deploy there; refuse it.
	tok, err := api.ProjectToken(ctx)
	if err != nil {
		return fmt.Errorf("read project token: %w", err)
	}
	if tok.EnvironmentID != env.RailwayEnvironmentID {
		return fmt.Errorf("the Railway token is scoped to environment %s, but %s is %s",
			tok.EnvironmentID, opts.env, env.RailwayEnvironmentID)
	}

	for _, svc := range f.Services {
		if svc.RailwayServiceID == "" {
			return fmt.Errorf("%s: release.yaml has no railwayServiceId", svc.Name)
		}
		want := svc.ImageRef()

		inst, err := api.ServiceInstance(ctx, svc.RailwayServiceID, env.RailwayEnvironmentID)
		if err != nil {
			return fmt.Errorf("%s: read service instance: %w", svc.Name, err)
		}
		if inst.Image == want && healthy(inst.LatestDeployment) {
			opts.logf("%s: %s is running; nothing to do", svc.Name, want)
			continue
		}
		opts.logf("%s: %s → %s (latest deployment: %s)", svc.Name, orNone(inst.Image), want, deploymentState(inst.LatestDeployment))
		if opts.dryRun {
			continue
		}

		if err := deployService(ctx, api, tok, env, svc, inst, opts); err != nil {
			return fmt.Errorf("%s: %w", svc.Name, err)
		}
		opts.logf("%s: %s deployed", svc.Name, want)
	}
	return nil
}

func deployService(ctx context.Context, api railwayAPI, tok railway.ProjectToken, env release.Environment,
	svc release.Service, inst railway.ServiceInstance, opts options,
) error {
	err := api.UpsertVariable(ctx, railway.Variable{
		ProjectID:     tok.ProjectID,
		EnvironmentID: env.RailwayEnvironmentID,
		ServiceID:     svc.RailwayServiceID,
		Name:          "SENTRY_RELEASE",
		Value:         svc.Name + "@" + strconv.Itoa(svc.Build),
	})
	if err != nil {
		return fmt.Errorf("set SENTRY_RELEASE: %w", err)
	}

	err = api.UpdateSource(ctx, railway.SourceUpdate{
		ServiceID:     svc.RailwayServiceID,
		EnvironmentID: env.RailwayEnvironmentID,
		Image:         svc.ImageRef(),
		NumReplicas:   inst.NumReplicas,
		Region:        inst.Region,
	})
	if err != nil {
		return fmt.Errorf("update source: %w", err)
	}

	id, err := api.Deploy(ctx, svc.RailwayServiceID, env.RailwayEnvironmentID)
	if err != nil {
		return fmt.Errorf("start deployment: %w", err)
	}
	opts.logf("%s: deployment %s started", svc.Name, id)
	return wait(ctx, api, id, opts)
}

// wait polls a deployment until it succeeds, fails, or times out.
func wait(ctx context.Context, api railwayAPI, id string, opts options) error {
	ctx, cancel := context.WithTimeoutCause(ctx, opts.timeout,
		fmt.Errorf("deployment %s not done after %s", id, opts.timeout))
	defer cancel()

	var last string
	pollErrors := 0
	for {
		status, err := api.DeploymentStatus(ctx, id)
		switch {
		case err != nil && ctx.Err() == nil:
			pollErrors++
			if pollErrors >= maxPollErrors {
				return fmt.Errorf("poll deployment %s: %w", id, err)
			}
			opts.logf("poll deployment %s: %v (retrying)", id, err)
		case err == nil:
			pollErrors = 0
			if status != last {
				opts.logf("deployment %s: %s", id, status)
				last = status
			}
			switch status {
			case railway.StatusSuccess:
				return nil
			case railway.StatusFailed, railway.StatusCrashed, railway.StatusRemoved, railway.StatusSkipped:
				return fmt.Errorf("deployment %s ended %s", id, status)
			}
		}

		select {
		case <-ctx.Done():
			if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
				return cause
			}
			return ctx.Err()
		case <-time.After(opts.pollInterval):
		}
	}
}

func healthy(d *railway.Deployment) bool {
	return d != nil && (d.Status == railway.StatusSuccess || d.Status == railway.StatusSleeping)
}

func deploymentState(d *railway.Deployment) string {
	if d == nil {
		return "none"
	}
	return d.Status
}

func orNone(s string) string {
	if s == "" {
		return "(no image)"
	}
	return s
}
