// Package ghcr reads what a published image says about itself: the commit it
// was built from, from its org.opencontainers.image.revision label.
//
// The images are public, so it pulls anonymously, as Railway does.
package ghcr

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Registry is GitHub's container registry.
const Registry = "https://ghcr.io"

const revisionLabel = "org.opencontainers.image.revision"

// Media types of a manifest list and of one image's manifest. GitHub Actions'
// buildx pushes an OCI index even for a single platform.
var acceptManifests = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// Client reads image metadata from a registry.
type Client struct {
	BaseURL    string // defaults to Registry
	HTTPClient *http.Client
}

// Revision returns the commit image:tag was built from. image is the
// repository without a tag, e.g. ghcr.io/ogen-app/ogen.
func (c *Client) Revision(ctx context.Context, image, tag string) (string, error) {
	repo, ok := strings.CutPrefix(image, "ghcr.io/")
	if !ok {
		return "", fmt.Errorf("%s: not a ghcr.io image", image)
	}
	base := cmp.Or(c.BaseURL, Registry)

	var tok struct {
		Token string `json:"token"`
	}
	if err := c.get(ctx, base+"/token?scope=repository:"+repo+":pull", "", "", &tok); err != nil {
		return "", fmt.Errorf("%s: pull token: %w", image, err)
	}

	var m manifest
	if err := c.get(ctx, base+"/v2/"+repo+"/manifests/"+tag, tok.Token, acceptManifests, &m); err != nil {
		return "", fmt.Errorf("%s:%s: manifest: %w", image, tag, err)
	}
	if len(m.Manifests) > 0 { // an index: pick the linux image
		digest := ""
		for _, d := range m.Manifests {
			if d.Platform.OS == "linux" {
				digest = d.Digest
				break
			}
		}
		if digest == "" {
			return "", fmt.Errorf("%s:%s: no linux image in the index", image, tag)
		}
		m = manifest{}
		if err := c.get(ctx, base+"/v2/"+repo+"/manifests/"+digest, tok.Token, acceptManifests, &m); err != nil {
			return "", fmt.Errorf("%s:%s: image manifest: %w", image, tag, err)
		}
	}
	if m.Config.Digest == "" {
		return "", fmt.Errorf("%s:%s: manifest has no config", image, tag)
	}

	var cfg struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if err := c.get(ctx, base+"/v2/"+repo+"/blobs/"+m.Config.Digest, tok.Token, "", &cfg); err != nil {
		return "", fmt.Errorf("%s:%s: config: %w", image, tag, err)
	}
	rev := cfg.Config.Labels[revisionLabel]
	if rev == "" {
		return "", fmt.Errorf("%s:%s: no %s label", image, tag, revisionLabel)
	}
	return rev, nil
}

type manifest struct {
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS string `json:"os"`
		} `json:"platform"`
	} `json:"manifests"`
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
}

func (c *Client) get(ctx context.Context, url, token, accept string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := cmp.Or(c.HTTPClient, http.DefaultClient).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.Join(errors.New("decode response"), err)
	}
	return nil
}
