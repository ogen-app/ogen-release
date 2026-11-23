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
// order. Railway is the truth for what runs (D7): a service is skipped only
// when its latest deployment is healthy and runs the pinned image, so a source
// update whose deploy never happened, or a manual rollback, is caught on the
// next run. The first failure stops the chain.
//
// Logs are public (the repo is public), so they name services and images but
// no Railway deployment ids.
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
		if running(inst, want) {
			opts.logf("%s: %s is running; nothing to do", svc.Name, want)
			continue
		}
		opts.logf("%s: %s → %s (latest deployment: %s)", svc.Name, runningImage(inst), want, deploymentState(inst.LatestDeployment))
		if inst.MultiRegionConfig == nil {
			return fmt.Errorf("%s: Railway reports no replicas or region for the service instance; "+
				"refusing to update it, since an update without them may reset them", svc.Name)
		}
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
		ServiceID:         svc.RailwayServiceID,
		EnvironmentID:     env.RailwayEnvironmentID,
		Image:             svc.ImageRef(),
		MultiRegionConfig: inst.MultiRegionConfig,
	})
	if err != nil {
		return fmt.Errorf("update source: %w", err)
	}

	id, err := api.Deploy(ctx, svc.RailwayServiceID, env.RailwayEnvironmentID)
	if err != nil {
		return fmt.Errorf("start deployment: %w", err)
	}
	opts.logf("%s: deployment started", svc.Name)
	return wait(ctx, api, svc.Name, id, opts)
}

// wait polls a deployment until it succeeds, fails, or times out.
func wait(ctx context.Context, api railwayAPI, name, id string, opts options) error {
	ctx, cancel := context.WithTimeoutCause(ctx, opts.timeout,
		fmt.Errorf("deployment not done after %s", opts.timeout))
	defer cancel()

	var last string
	pollErrors := 0
	for {
		status, err := api.DeploymentStatus(ctx, id)
		switch {
		case err != nil && ctx.Err() == nil:
			pollErrors++
			if pollErrors >= maxPollErrors {
				return fmt.Errorf("poll deployment: %w", err)
			}
			opts.logf("%s: poll deployment: %v (retrying)", name, err)
		case err == nil:
			pollErrors = 0
			if status != last {
				opts.logf("%s: deployment %s", name, status)
				last = status
			}
			switch status {
			case railway.StatusSuccess:
				return nil
			case railway.StatusFailed, railway.StatusCrashed, railway.StatusRemoved, railway.StatusSkipped:
				return fmt.Errorf("deployment ended %s", status)
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

// running reports whether the instance is configured for want and its latest
// deployment is healthy and runs want. The configured image alone isn't proof:
// it changes before the deploy does. A deployment whose image Railway doesn't
// report counts as not running want, which redeploys rather than skips.
func running(inst railway.ServiceInstance, want string) bool {
	d := inst.LatestDeployment
	return inst.Image == want && d != nil && d.Image == want &&
		(d.Status == railway.StatusSuccess || d.Status == railway.StatusSleeping)
}

func runningImage(inst railway.ServiceInstance) string {
	if d := inst.LatestDeployment; d != nil && d.Image != "" {
		return d.Image
	}
	if inst.Image != "" {
		return inst.Image + " (configured)"
	}
	return "(no image)"
}

func deploymentState(d *railway.Deployment) string {
	if d == nil {
		return "none"
	}
	return d.Status
}
