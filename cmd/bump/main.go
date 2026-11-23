// Command bump pins a new build of one service in release.yaml.
//
// It is the body of .github/workflows/bump.yml: service CI dispatches
// `bump {service, build, revision}` after pushing an image, and this command
// writes the new build. Dispatches aren't ordered, so a build at or below the
// pinned one is ignored; a late or replayed dispatch never downgrades. So is a
// service release.yaml doesn't list yet.
//
// On a bump it prints the commit message to stdout. On a no-op it prints
// nothing to stdout and exits 0.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"

	"github.com/ogen-app/ogen-release/internal/release"
)

var (
	revisionRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// serviceRE is what a release.yaml service name looks like. The name comes
	// from an untrusted dispatch payload and is logged, so it's checked first.
	serviceRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("bump: ")

	file := flag.String("file", "release.yaml", "path to release.yaml")
	service := flag.String("service", "", "service name in release.yaml")
	build := flag.Int("build", 0, "new build number")
	revision := flag.String("revision", "", "commit SHA the build was made from")
	flag.Parse()

	msg, err := run(*file, *service, *build, *revision)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(msg)
}

func run(file, service string, build int, revision string) (string, error) {
	if build <= 0 {
		return "", fmt.Errorf("build must be a positive integer, got %d", build)
	}
	if !revisionRE.MatchString(revision) {
		return "", fmt.Errorf("revision must be a full lowercase commit SHA, got %q", revision)
	}
	if !serviceRE.MatchString(service) {
		return "", fmt.Errorf("service must be a release.yaml service name (lowercase letters, digits, hyphens), got %q", service)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	f, err := release.Parse(data)
	if err != nil {
		return "", err
	}
	// A repo publishes and dispatches before its service joins release.yaml
	// (ui until its image can run in production), so an unlisted service is a
	// no-op, not a failed run on every merge there.
	svc, ok := f.Service(service)
	if !ok {
		log.Printf("%s is not in release.yaml; nothing to do", service)
		return "", nil
	}
	if build <= svc.Build {
		log.Printf("%s: build %d is not newer than pinned %d; nothing to do", service, build, svc.Build)
		return "", nil
	}

	out, err := f.SetBuild(service, build)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(file, out, 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("fix(deps): %s %d → %d\n\nRevision: %s\n", service, svc.Build, build, revision), nil
}
