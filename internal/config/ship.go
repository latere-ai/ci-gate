// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Defaults of the ship block.
const (
	DefaultShipDockerfile = "Dockerfile"
	DefaultShipContext    = "."
	DefaultShipPlatform   = "linux/amd64"
	DefaultShipTimeout    = "300s"
	DefaultShipSmoke      = "tools/smoke/release.sh"
)

// Ship says a repository builds, publishes and deploys its release from a
// workstation rather than from GitHub Actions. Its presence is the
// declaration: `lateregate release` then reads no CI, the wiring check asks
// for no push workflow, and `build`, `push` and `deploy` have what they act
// on.
type Ship struct {
	Image  ShipImage  `yaml:"image"`
	Deploy ShipDeploy `yaml:"deploy"`
}

// ShipImage is the image a release publishes.
type ShipImage struct {
	// Name is the registry path without a tag.
	Name string `yaml:"name"`
	// Dockerfile and Context are relative to the repository root.
	Dockerfile string `yaml:"dockerfile"`
	Context    string `yaml:"context"`
	// Platform is the cluster's platform, linux/<arch>.
	Platform  string            `yaml:"platform"`
	BuildArgs map[string]string `yaml:"build_args"`
	// VersionArg is the build argument that receives the version.
	VersionArg string `yaml:"version_arg"`
	// Prepare lists shell commands run from the repository root before the
	// image builds, with VERSION, COMMIT, GOOS and GOARCH in the
	// environment: for a Dockerfile that copies a prebuilt binary.
	Prepare []string `yaml:"prepare"`
}

// ShipDeploy is where the release goes and how it is proved live.
type ShipDeploy struct {
	// Kubeconfig is a 1Password secret reference to the rollout identity's
	// kubeconfig.
	Kubeconfig string `yaml:"kubeconfig"`
	Namespace  string `yaml:"namespace"`
	Deployment string `yaml:"deployment"`
	// Container defaults to the deployment's name.
	Container      string `yaml:"container"`
	RolloutTimeout string `yaml:"rollout_timeout"`
	// URL is the live check's BASE_URL.
	URL string `yaml:"url"`
	// Smoke is the live check, read from the tag.
	Smoke string `yaml:"smoke"`
	// Preflight runs before the apply, AfterRoll after the rollout, each a
	// list of shell commands run from the tag's tree.
	Preflight []string `yaml:"preflight"`
	AfterRoll []string `yaml:"after_roll"`
}

// Ships reports whether the repository builds and ships from a workstation.
func (c *Config) Ships() bool { return c.Ship != nil }

// ContainerOr is the container the image is set on.
func (d ShipDeploy) ContainerOr() string {
	if d.Container == "" {
		return d.Deployment
	}
	return d.Container
}

// Arch is the architecture of the platform: amd64 for linux/amd64, arm for
// linux/arm/v7.
func (i ShipImage) Arch() string {
	_, rest, _ := strings.Cut(i.Platform, "/")
	arch, _, _ := strings.Cut(rest, "/")
	return arch
}

// BuildArgKeys is BuildArgs' keys in order.
func (i ShipImage) BuildArgKeys() []string { return sortedKeys(i.BuildArgs) }

var (
	shipPlatform = regexp.MustCompile(`^linux/(amd64|arm64|386|ppc64le|s390x|riscv64|arm/v[5-8])$`)
	shipName     = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+$`)
	shipIdent    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	shipK8sName  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
)

// defaults fills the values a repository need not decide and records the
// ones the file restated.
func (s *Ship) defaults(restated *[]string) {
	fill := func(v *string, def, key string) {
		switch *v {
		case "":
			*v = def
		case def:
			*restated = append(*restated, key)
		}
	}
	fill(&s.Image.Dockerfile, DefaultShipDockerfile, "ship.image.dockerfile")
	fill(&s.Image.Context, DefaultShipContext, "ship.image.context")
	fill(&s.Image.Platform, DefaultShipPlatform, "ship.image.platform")
	fill(&s.Deploy.RolloutTimeout, DefaultShipTimeout, "ship.deploy.rollout_timeout")
	fill(&s.Deploy.Smoke, DefaultShipSmoke, "ship.deploy.smoke")
}

// validate rejects a block that could not build, push or deploy.
func (s *Ship) validate() error {
	i, d := s.Image, s.Deploy
	if i.Name == "" {
		return errors.New("ship.image.name is required: the registry path the release publishes, such as ghcr.io/latere-ai/auth")
	}
	if !shipName.MatchString(i.Name) {
		return fmt.Errorf("ship.image.name %q must be a registry path with no tag or digest, such as ghcr.io/latere-ai/auth", i.Name)
	}
	for key, p := range map[string]string{"ship.image.dockerfile": i.Dockerfile, "ship.image.context": i.Context, "ship.deploy.smoke": d.Smoke} {
		if p != "" && !filepath.IsLocal(filepath.Clean(p)) {
			return fmt.Errorf("%s: %q must be a path inside the repository", key, p)
		}
	}
	if i.Platform != "" && !shipPlatform.MatchString(i.Platform) {
		return fmt.Errorf("ship.image.platform %q is not a Linux platform: write linux/amd64 or linux/arm64", i.Platform)
	}
	if i.VersionArg != "" {
		if !shipIdent.MatchString(i.VersionArg) {
			return fmt.Errorf("ship.image.version_arg %q is not a build argument name", i.VersionArg)
		}
		if _, ok := i.BuildArgs[i.VersionArg]; ok {
			return fmt.Errorf("ship.image.build_args sets %s, which version_arg already fills with the version", i.VersionArg)
		}
	}
	for k := range i.BuildArgs {
		if !shipIdent.MatchString(k) {
			return fmt.Errorf("ship.image.build_args: %q is not a build argument name", k)
		}
	}
	if err := commands("ship.image.prepare", i.Prepare); err != nil {
		return err
	}
	if err := commands("ship.deploy.preflight", d.Preflight); err != nil {
		return err
	}
	if err := commands("ship.deploy.after_roll", d.AfterRoll); err != nil {
		return err
	}
	if err := d.validate(); err != nil {
		return err
	}
	return nil
}

func (d ShipDeploy) validate() error {
	if d.Kubeconfig == "" {
		return errors.New("ship.deploy.kubeconfig is required: a 1Password reference to the rollout identity's kubeconfig, op://VAULT/ITEM/FILE")
	}
	if rest, ok := strings.CutPrefix(d.Kubeconfig, "op://"); !ok {
		return fmt.Errorf("ship.deploy.kubeconfig must be a 1Password secret reference starting with op://; the kubeconfig itself never belongs in this file")
	} else if parts := strings.Split(rest, "/"); len(parts) < 3 || slicesAnyEmpty(parts) {
		return errors.New("ship.deploy.kubeconfig must name a vault, an item and a file: op://VAULT/ITEM/FILE")
	}
	for key, v := range map[string]string{"ship.deploy.namespace": d.Namespace, "ship.deploy.deployment": d.Deployment} {
		if v == "" {
			return fmt.Errorf("%s is required", key)
		}
	}
	for key, v := range map[string]string{"ship.deploy.namespace": d.Namespace, "ship.deploy.deployment": d.Deployment, "ship.deploy.container": d.Container} {
		if v != "" && !shipK8sName.MatchString(v) {
			return fmt.Errorf("%s %q is not a Kubernetes name", key, v)
		}
	}
	if d.URL == "" {
		return errors.New("ship.deploy.url is required: the production address the live check runs against")
	}
	u, err := url.Parse(d.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("ship.deploy.url %q must be an https address", d.URL)
	}
	if d.RolloutTimeout != "" {
		if t, err := time.ParseDuration(d.RolloutTimeout); err != nil || t <= 0 {
			return fmt.Errorf("ship.deploy.rollout_timeout %q must be a positive duration such as 300s or 5m", d.RolloutTimeout)
		}
	}
	return nil
}

func slicesAnyEmpty(parts []string) bool {
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return true
		}
	}
	return false
}

func commands(key string, list []string) error {
	for i, c := range list {
		if strings.TrimSpace(c) == "" {
			return fmt.Errorf("%s[%d] is an empty command", key, i)
		}
	}
	return nil
}
