// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// prepare runs the commands that put the image's inputs in place, from the
// repository root.
func (d Deps) prepare(version string) error {
	s, err := d.image()
	if err != nil {
		return err
	}
	if len(s.Image.Prepare) == 0 {
		return nil
	}
	commit, err := d.git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	env := withEnv(d.Environ(),
		"VERSION="+version, "COMMIT="+commit, "GOOS=linux", "GOARCH="+s.Image.Arch())
	return script(d.Exec, env, "ship.image.prepare", s.Image.Prepare)
}

// buildImage prepares and builds one image carrying every tag given.
func (d Deps) buildImage(engine, version string, tags []string) error {
	s, err := d.image()
	if err != nil {
		return err
	}
	if err := d.prepare(version); err != nil {
		return err
	}
	i := s.Image
	args := []string{"build", "--platform", i.Platform, "-f", i.Dockerfile}
	for _, t := range tags {
		args = append(args, "-t", t)
	}
	for _, k := range i.BuildArgKeys() {
		args = append(args, "--build-arg", k+"="+i.BuildArgs[k])
	}
	if i.VersionArg != "" {
		args = append(args, "--build-arg", i.VersionArg+"="+version)
	}
	args = append(args, i.Context)
	d.printf("%s %s\n", engine, strings.Join(args, " "))
	if _, err := d.Exec(nil, true, engine, args...); err != nil {
		return fmt.Errorf("the image does not build: %w", err)
	}
	return nil
}

// Build builds the deploy image for the cluster's platform and leaves it in
// the local engine, tagged dev-<short sha>. The whole bar runs before this,
// in the caller.
func Build(d Deps) error {
	s, err := d.ship()
	if err != nil {
		return err
	}
	if !s.Builds() {
		d.printf("this repository builds no image\n")
		return nil
	}
	engine, err := d.engine()
	if err != nil {
		return err
	}
	version, err := d.git("describe", "--tags", "--always", "--dirty")
	if err != nil {
		return err
	}
	sha, err := d.git("rev-parse", "--short", "HEAD")
	if err != nil {
		return err
	}
	tag := s.Image.Name + ":dev-" + sha
	if err := d.buildImage(engine, version, []string{tag}); err != nil {
		return err
	}
	d.printf("built %s for %s as version %s\n", tag, s.Image.Platform, version)
	return nil
}

// headTag is the release tag HEAD sits on, the highest when several do.
func (d Deps) headTag() (string, error) {
	out, err := d.git("tag", "--points-at", "HEAD")
	if err != nil {
		return "", err
	}
	best := ""
	for t := range strings.FieldsSeq(out) {
		if stable(t) && (best == "" || semverLess(best, t)) {
			best = t
		}
	}
	if best == "" {
		return "", errNoTag
	}
	return best, nil
}

var digestIn = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

// Push builds the image from the tagged commit and publishes it. HEAD must be
// exactly a release tag on a clean tree: no bar runs, because the release that
// cut the tag ran it on this tree.
func Push(d Deps) error {
	s, err := d.image()
	if err != nil {
		return err
	}
	tag, err := d.headTag()
	if err != nil {
		return fmt.Errorf("HEAD is not at a release tag, so there is no version to publish: check out vX.Y.Z, or cut one with `lateregate release vX.Y.Z`")
	}
	dirty, err := d.git("status", "--porcelain")
	if err != nil {
		return err
	}
	if dirty != "" {
		return fmt.Errorf("the working tree is not clean, and an image built from it would not be %s:\n%s", tag, dirty)
	}
	engine, err := d.engine()
	if err != nil {
		return err
	}
	sha, err := d.git("rev-parse", "--short", "HEAD")
	if err != nil {
		return err
	}
	name := s.Image.Name
	tags := []string{name + ":" + tag, name + ":sha-" + sha}
	if !prerelease(tag) {
		tags = append(tags, name+":latest")
	}
	if err := d.buildImage(engine, tag, tags); err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "lateregate-push")
	if err != nil {
		return fmt.Errorf("making a directory for the pushed digest: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	digestFile := filepath.Join(dir, "digest")

	digest := ""
	for i, t := range tags {
		args := []string{"push"}
		if engine == "podman" && i == 0 {
			args = append(args, "--digestfile", digestFile)
		}
		args = append(args, t)
		d.printf("%s %s\n", engine, strings.Join(args, " "))
		out, err := combined(d.Exec, nil, engine, args...)
		_, _ = d.Out.Write(out)
		if err != nil {
			if authFailure.Match(out) {
				return registryRefused(engine, registryOf(name), "push")
			}
			return fmt.Errorf("%s push %s: %w", engine, t, err)
		}
		if i == 0 && digest == "" {
			digest = digestIn.FindString(string(out))
		}
	}
	if b, err := os.ReadFile(digestFile); err == nil {
		if got := digestIn.FindString(string(b)); got != "" {
			digest = got
		}
	}
	if digest == "" {
		return fmt.Errorf("pushed %s, but %s did not report its digest; `lateregate deploy %s` resolves it from the registry", strings.Join(tags, ", "), engine, tag)
	}
	d.printf("pushed %s@%s as %s\n", name, digest, strings.Join(tags, ", "))
	return nil
}
