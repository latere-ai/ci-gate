// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/mod/semver"

	"latere.ai/x/ci-gate/internal/gates"
)

func semverLess(a, b string) bool { return semver.Compare(a, b) < 0 }

// NewestTag is the highest stable vX.Y.Z tag on origin, by version order.
func (d Deps) NewestTag() (string, error) {
	out, err := d.Exec(nil, false, "git", "ls-remote", "--tags", "origin")
	if err != nil {
		return "", fmt.Errorf("listing origin's tags: %w", err)
	}
	best := ""
	for line := range strings.SplitSeq(string(out), "\n") {
		_, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || strings.HasSuffix(ref, "^{}") {
			continue
		}
		t, ok := strings.CutPrefix(ref, "refs/tags/")
		if !ok || !stable(t) || prerelease(t) {
			continue
		}
		if best == "" || semverLess(best, t) {
			best = t
		}
	}
	if best == "" {
		return "", errors.New("origin has no stable vX.Y.Z tag to deploy; run `lateregate release vX.Y.Z` first")
	}
	return best, nil
}

// commitOf is the commit a tag names, fetching the tag when it is not local.
func (d Deps) commitOf(tag string) (string, error) {
	rev := "refs/tags/" + tag + "^{commit}"
	if sha, err := d.git("rev-parse", "--verify", rev); err == nil {
		return sha, nil
	}
	if _, err := d.Exec(nil, true, "git", "fetch", "origin", "refs/tags/"+tag+":refs/tags/"+tag); err != nil {
		return "", fmt.Errorf("%s is neither a local tag nor one origin has: %w", tag, err)
	}
	return d.git("rev-parse", "--verify", rev)
}

var notFound = regexp.MustCompile(`(?i)not found|manifest unknown|name unknown|no such manifest|does not exist|404`)

// Digest resolves the digest of ref in the registry with the engine's own
// commands: docker reads the manifest, podman pulls the image and reads the
// digest of the manifest it pulled. No other tool is needed.
func (d Deps) digest(engine, ref string) (string, error) {
	s, err := d.image()
	if err != nil {
		return "", err
	}
	var out []byte
	if engine == "docker" {
		out, err = combined(d.Exec, nil, "docker", "buildx", "imagetools", "inspect", ref)
		if err == nil {
			m := regexp.MustCompile(`(?m)^Digest:\s+(sha256:[0-9a-f]{64})`).FindSubmatch(out)
			if m == nil {
				return "", fmt.Errorf("docker did not report a digest for %s", ref)
			}
			return string(m[1]), nil
		}
	} else {
		out, err = combined(d.Exec, nil, engine, "pull", "--quiet", "--platform", s.Image.Platform, ref)
		if err == nil {
			var ins []byte
			// The first line is the digest of the manifest the pull just
			// fetched from the registry. The lines after it are every
			// digest the engine lists for the image, which can include one
			// computed locally at build time under the same name that the
			// registry does not have, so the pulled digest is the one used,
			// and only when it is listed under this image's own name.
			ins, err = combined(d.Exec, nil, engine, "image", "inspect", "--format", "{{.Digest}}\n{{range .RepoDigests}}{{println .}}{{end}}", ref)
			out = ins
			if err == nil {
				first, rest, _ := strings.Cut(string(ins), "\n")
				pulled := strings.TrimSpace(first)
				if !digestIn.MatchString(pulled) || digestIn.FindString(pulled) != pulled {
					return "", fmt.Errorf("%s reported no digest for the pull of %s", engine, ref)
				}
				for line := range strings.SplitSeq(rest, "\n") {
					if strings.TrimSpace(line) == s.Image.Name+"@"+pulled {
						return pulled, nil
					}
				}
				return "", fmt.Errorf("%s pulled %s as %s, which it does not list under %s", engine, ref, pulled, s.Image.Name)
			}
		}
	}
	if authFailure.Match(out) {
		return "", registryRefused(engine, registryOf(s.Image.Name), "read")
	}
	if notFound.Match(out) {
		return "", errNotInRegistry
	}
	return "", fmt.Errorf("%s could not resolve %s: %w: %s", engine, ref, err, firstLine(string(out)))
}

var errNotInRegistry = errors.New("not in the registry")

// extract unpacks a tar stream under dest, refusing any path that leaves it.
func extract(archive []byte, dest string) error {
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the tag's archive: %w", err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		clean := filepath.Clean(h.Name)
		if !filepath.IsLocal(clean) && clean != "." {
			return fmt.Errorf("the archive holds %q, which is outside the tree", h.Name)
		}
		p := filepath.Join(dest, clean)
		if err := refuseThroughSymlink(dest, p, h.Name); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("unpacking %s: %w", h.Name, err)
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, p); err != nil {
				return err
			}
		}
	}
}

// refuseThroughSymlink refuses an entry whose path, below dest, passes through
// a symlink the archive created earlier: writing there would land wherever the
// link points, which may be outside dest. The entry's own path is checked too,
// so a file cannot replace a link by being written through it.
func refuseThroughSymlink(dest, p, name string) error {
	rel, err := filepath.Rel(dest, p)
	if err != nil || rel == "." {
		return err
	}
	cur := dest
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("the archive holds %q under a symlink, which could write outside the tree", name)
		}
	}
	return nil
}

// secrets holds the temporary directory a deploy keeps its credential and
// the tag's tree in, and removes it once, whichever path asks first.
type secrets struct {
	dir  string
	once sync.Once
	err  error
}

func (s *secrets) remove() error {
	s.once.Do(func() { s.err = os.RemoveAll(s.dir) })
	return s.err
}

// guard removes s when the process is told to stop, and returns the function
// that ends the listening.
func (d Deps) guard(s *secrets) func() {
	sigs := d.Signals
	stop := func() {}
	if sigs == nil {
		sigs = make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		stop = func() { signal.Stop(sigs) }
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-sigs:
			_ = s.remove()
			d.printf("interrupted: the temporary kubeconfig is removed\n")
			exit := d.Exit
			if exit == nil {
				exit = os.Exit
			}
			exit(130)
		case <-done:
		}
	}()
	return func() {
		stop()
		close(done)
		<-finished
	}
}

// readKubeconfig writes the secret the reference names to path with 1Password's
// own file output, so the content never passes through this process.
func (d Deps) readKubeconfig(ref, path string) error {
	if _, err := d.LookPath("op"); err != nil {
		return fmt.Errorf("the 1Password CLI (op) is not on PATH; deploy reads the rollout kubeconfig at %s with it", ref)
	}
	out, err := combined(d.Exec, nil, "op", "read", "--out-file", path, "--file-mode", "0600", ref)
	if err != nil {
		return fmt.Errorf("`op read` could not read %s: %w: %s", ref, err, firstLine(string(out)))
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("`op read` wrote nothing at %s for %s: %w", path, ref, err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		return fmt.Errorf("%s is an empty kubeconfig", ref)
	}
	return nil
}

// Deploy rolls the tag out to the cluster and proves it live. tagArg is a
// vX.Y.Z, or empty for the newest stable tag on origin.
func Deploy(d Deps, tagArg string) (err error) {
	s, err := d.deployment()
	if err != nil {
		return err
	}
	dep := s.Deploy
	tag := tagArg
	if tag == "" {
		if tag, err = d.NewestTag(); err != nil {
			return err
		}
	} else if !stable(tag) {
		return fmt.Errorf("%q is not a release tag: write vMAJOR.MINOR.PATCH", tag)
	}
	commit, err := d.commitOf(tag)
	if err != nil {
		return err
	}
	engine, err := d.engine()
	if err != nil {
		return err
	}
	ref := s.Image.Name + ":" + tag
	sum, err := d.digest(engine, ref)
	switch {
	case errors.Is(err, errNotInRegistry):
		return fmt.Errorf("the image for %s is not in the registry; `lateregate push` at that tag builds and publishes it", tag)
	case err != nil:
		return err
	}
	image := s.Image.Name + "@" + sum

	archive, err := d.Exec(nil, false, "git", "archive", "--format=tar", "refs/tags/"+tag)
	if err != nil {
		return fmt.Errorf("git archive %s: %w", tag, err)
	}
	dir, err := os.MkdirTemp("", "lateregate-deploy")
	if err != nil {
		return fmt.Errorf("making the deploy directory: %w", err)
	}
	sec := &secrets{dir: dir}
	defer d.guard(sec)()
	defer func() {
		if rmErr := sec.remove(); rmErr != nil && err == nil {
			err = fmt.Errorf("removing %s: %w", dir, rmErr)
		}
	}()
	tree := filepath.Join(dir, "tree")
	if err := extract(archive, tree); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(tree, dep.Smoke)); err != nil {
		return fmt.Errorf("the live check %s is not in %s: a deploy without one is refused, not skipped", dep.Smoke, tag)
	}
	if info, err := os.Stat(filepath.Join(tree, "deploy", "prod")); err != nil || !info.IsDir() {
		return fmt.Errorf("deploy/prod is not in %s", tag)
	}

	kubeconfig := filepath.Join(dir, "kubeconfig")
	if err := d.readKubeconfig(dep.Kubeconfig, kubeconfig); err != nil {
		return err
	}
	// The steps know what they deploy: a preflight that checks the tag's pins
	// or an after_roll that applies something tagged reads it here.
	env := withEnv(d.Environ(), "KUBECONFIG="+kubeconfig, "TAG="+tag, "COMMIT="+commit, "IMAGE="+image)
	run := d.In(tree)
	kubectl := func(args ...string) error {
		_, err := run(env, true, "kubectl", append([]string{"--kubeconfig", kubeconfig}, args...)...)
		return err
	}

	step := func(name string, f func() error) error {
		d.printf("== %s\n", name)
		if err := f(); err != nil {
			return fmt.Errorf("deploy of %s stopped at %s: %w", tag, name, err)
		}
		return nil
	}
	if err := step("preflight", func() error { return script(run, env, "ship.deploy.preflight", dep.Preflight) }); err != nil {
		return err
	}
	if err := step("apply deploy/prod", func() error {
		flag := "-f"
		if _, err := os.Stat(filepath.Join(tree, "deploy", "prod", "kustomization.yaml")); err == nil {
			flag = "-k"
		}
		return kubectl("apply", flag, "deploy/prod/")
	}); err != nil {
		return err
	}
	if err := step("set image", func() error {
		return kubectl("-n", dep.Namespace, "set", "image", "deployment/"+dep.Deployment, dep.ContainerOr()+"="+image)
	}); err != nil {
		return err
	}
	if err := step("rollout status", func() error {
		return kubectl("-n", dep.Namespace, "rollout", "status", "deployment/"+dep.Deployment, "--timeout="+dep.RolloutTimeout)
	}); err != nil {
		return err
	}
	if err := step("after_roll", func() error { return script(run, env, "ship.deploy.after_roll", dep.AfterRoll) }); err != nil {
		return err
	}
	if err := step("live check", func() error {
		// No repository has smoke credentials, so SERVICE_TOKEN is left out.
		// The rest of the operator's environment is passed on as it is.
		smokeEnv := withEnv(without(env, "SERVICE_TOKEN"),
			"BASE_URL="+dep.URL, "TAG="+tag, "COMMIT="+commit,
			"OUTPUT_MD="+filepath.Join(dir, "smoke.md"))
		return d.liveCheck(run, smokeEnv, "./"+filepath.ToSlash(dep.Smoke))
	}); err != nil {
		return err
	}
	d.printf("deployed %s (%s) to %s/%s; live check passed at %s\n", tag, sum, dep.Namespace, dep.Deployment, dep.URL)

	if err := PublishRelease(d, tag); err != nil {
		return fmt.Errorf("%s is deployed and live, but its GitHub release was not published: %w", tag, err)
	}
	return nil
}

// PublishRelease creates the GitHub release for tag with the changelog
// section as its notes, unless it exists. It is safe to run again.
func PublishRelease(d Deps, tag string) error {
	if !stable(tag) {
		return fmt.Errorf("%q is not a release tag", tag)
	}
	out, err := combined(d.Exec, nil, "gh", "release", "view", tag)
	if err == nil {
		d.printf("GitHub release %s exists\n", tag)
		return nil
	}
	if !notFound.Match(out) {
		return fmt.Errorf("checking for the GitHub release %s: %w: %s", tag, err, firstLine(string(out)))
	}
	notes, err := changelogNotes(d, tag)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "lateregate-notes-*.md")
	if err != nil {
		return fmt.Errorf("writing the release notes: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(notes); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing the release notes: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing the release notes: %w", err)
	}
	args := []string{"release", "create", tag, "--title", tag, "--notes-file", f.Name(), "--verify-tag"}
	if prerelease(tag) {
		args = append(args, "--prerelease")
	}
	if out, err := combined(d.Exec, nil, "gh", args...); err != nil {
		return fmt.Errorf("`gh release create %s`: %w: %s", tag, err, firstLine(string(out)))
	}
	d.printf("published the GitHub release %s\n", tag)
	return nil
}

// liveCheck runs the smoke script, and again after each failure every
// LiveCheckInterval until it passes or LiveCheckWindow has passed since the
// first attempt. Every attempt streams to the operator. The error carries the
// last attempt's output and how many attempts ran.
func (d Deps) liveCheck(run gates.Exec, env []string, script string) error {
	start := d.Now()
	for attempt := 1; ; attempt++ {
		out, err := run(env, true, script)
		if err == nil {
			return nil
		}
		if d.Now().Sub(start)+LiveCheckInterval > LiveCheckWindow {
			msg := fmt.Sprintf("the live check failed %d times over %s: %v", attempt, LiveCheckWindow, err)
			if last := strings.TrimSpace(string(out)); last != "" {
				msg += "\nlast attempt:\n" + last
			}
			return errors.New(msg)
		}
		d.printf("live check attempt %d failed: %v; trying again in %s\n", attempt, err, LiveCheckInterval)
		d.Sleep(LiveCheckInterval)
	}
}
