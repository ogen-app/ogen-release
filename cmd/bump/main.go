// Command bump pins a new build of one service in release.yaml.
//
// It is the body of .github/workflows/bump.yml: service CI dispatches
// `bump {service, build, revision}` after pushing an image, and this command
// writes the new build. Dispatches aren't ordered, so a build at or below the
// pinned one is ignored; a late or replayed dispatch never downgrades.
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

var revisionRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

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

	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	f, err := release.Parse(data)
	if err != nil {
		return "", err
	}
	svc, ok := f.Service(service)
	if !ok {
		return "", fmt.Errorf("unknown service %q", service)
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
