// Command deploy deploys release.yaml to a Railway environment.
//
//	deploy --env development|production [--ref <git ref>] [--dry-run]
//
// It reads release.yaml at --ref (production deploys the tag) and, for each
// service in file order whose Railway instance doesn't already run the pinned
// image: sets SENTRY_RELEASE, points the instance at <image>:<build>, deploys,
// and waits for SUCCESS. A failed, crashed or timed-out deployment stops the
// chain.
//
// RAILWAY_TOKEN must be a project token scoped to the target environment.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"time"

	"github.com/ogen-app/ogen-release/internal/railway"
	"github.com/ogen-app/ogen-release/internal/release"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("deploy: ")

	env := flag.String("env", "", "environment in release.yaml: development or production")
	ref := flag.String("ref", "HEAD", "git ref whose release.yaml to deploy")
	dryRun := flag.Bool("dry-run", false, "print what would be deployed and change nothing")
	timeout := flag.Duration("timeout", 15*time.Minute, "how long one service's deployment may take")
	flag.Parse()

	if *env != "development" && *env != "production" {
		log.Fatal("--env must be development or production")
	}
	token := os.Getenv("RAILWAY_TOKEN")
	if token == "" {
		log.Fatal("RAILWAY_TOKEN is not set")
	}

	data, err := exec.Command("git", "show", *ref+":release.yaml").Output()
	if err != nil {
		log.Fatalf("read release.yaml at %s: %v", *ref, err)
	}
	f, err := release.Parse(data)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := &railway.Client{Token: token, HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	err = deploy(ctx, client, f, options{
		env:          *env,
		dryRun:       *dryRun,
		pollInterval: 5 * time.Second,
		timeout:      *timeout,
		logf:         log.Printf,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "::error::%v\n", err)
		os.Exit(1)
	}
}
