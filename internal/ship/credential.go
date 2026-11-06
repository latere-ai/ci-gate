// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"bytes"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
)

// IdentityFile is where a repository keeps its rollout identity.
const IdentityFile = "deploy/bootstrap/rollout-identity.yaml"

const tokenSecretType = "kubernetes.io/service-account-token"

// identity is the ServiceAccount a repository rolls out as and the Secret that
// holds its token.
type identity struct {
	account, namespace, secret string
}

// readIdentity finds the ServiceAccount and its token Secret in the file.
func readIdentity(body []byte) (identity, error) {
	type object struct {
		Kind     string `yaml:"kind"`
		Type     string `yaml:"type"`
		Metadata struct {
			Name        string            `yaml:"name"`
			Namespace   string            `yaml:"namespace"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"metadata"`
	}
	var accounts, secrets []object
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var o object
		if err := dec.Decode(&o); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return identity{}, fmt.Errorf("%s: %w", IdentityFile, err)
		}
		switch o.Kind {
		case "ServiceAccount":
			accounts = append(accounts, o)
		case "Secret":
			if o.Type == tokenSecretType {
				secrets = append(secrets, o)
			}
		}
	}
	if len(accounts) == 0 {
		return identity{}, fmt.Errorf("%s holds no ServiceAccount", IdentityFile)
	}
	if len(accounts) > 1 {
		return identity{}, fmt.Errorf("%s holds %d ServiceAccounts; the rollout identity is one", IdentityFile, len(accounts))
	}
	sa := accounts[0]
	if sa.Metadata.Name == "" || sa.Metadata.Namespace == "" {
		return identity{}, fmt.Errorf("%s: the ServiceAccount needs a name and a namespace", IdentityFile)
	}
	for _, s := range secrets {
		if s.Metadata.Annotations["kubernetes.io/service-account.name"] == sa.Metadata.Name {
			return identity{account: sa.Metadata.Name, namespace: sa.Metadata.Namespace, secret: s.Metadata.Name}, nil
		}
	}
	return identity{}, fmt.Errorf("%s holds no Secret of type %s annotated kubernetes.io/service-account.name: %s",
		IdentityFile, tokenSecretType, sa.Metadata.Name)
}

type kubeconfig struct {
	APIVersion     string         `yaml:"apiVersion"`
	Kind           string         `yaml:"kind"`
	CurrentContext string         `yaml:"current-context"`
	Clusters       []kubeCluster  `yaml:"clusters"`
	Users          []kubeUser     `yaml:"users"`
	Contexts       []kubeContext  `yaml:"contexts"`
	Preferences    map[string]any `yaml:"preferences"`
}

type kubeCluster struct {
	Name    string `yaml:"name"`
	Cluster struct {
		Server string `yaml:"server"`
		CAData string `yaml:"certificate-authority-data"`
	} `yaml:"cluster"`
}

type kubeUser struct {
	Name string `yaml:"name"`
	User struct {
		Token string `yaml:"token"`
	} `yaml:"user"`
}

type kubeContext struct {
	Name    string `yaml:"name"`
	Context struct {
		Cluster   string `yaml:"cluster"`
		User      string `yaml:"user"`
		Namespace string `yaml:"namespace"`
	} `yaml:"context"`
}

// render writes the one-cluster, one-user, one-context kubeconfig.
func render(id identity, server, ca, token string) ([]byte, error) {
	k := kubeconfig{APIVersion: "v1", Kind: "Config", CurrentContext: id.account, Preferences: map[string]any{}}
	c := kubeCluster{Name: id.account}
	c.Cluster.Server, c.Cluster.CAData = server, ca
	u := kubeUser{Name: id.account}
	u.User.Token = token
	x := kubeContext{Name: id.account}
	x.Context.Cluster, x.Context.User, x.Context.Namespace = id.account, id.account, id.namespace
	k.Clusters, k.Users, k.Contexts = []kubeCluster{c}, []kubeUser{u}, []kubeContext{x}
	return yaml.Marshal(k)
}

var opRef = regexp.MustCompile(`^op://([^/]+)/([^/]+)/([^/]+)$`)

// DeployCredential is the one-time operator step that stores a repository's
// rollout identity as the 1Password Document `deploy` reads. kubeContext names
// the operator's own admin context, the only cluster access it uses besides
// the identity it is storing.
func DeployCredential(d Deps, kubeContext string) (err error) {
	s, err := d.deployment()
	if err != nil {
		return err
	}
	if strings.TrimSpace(kubeContext) == "" {
		return errors.New("-context is required: name the kubeconfig context of an administrator of the cluster")
	}
	m := opRef.FindStringSubmatch(s.Deploy.Kubeconfig)
	if m == nil {
		return fmt.Errorf("ship.deploy.kubeconfig %q must be op://VAULT/ITEM/FILE to name a Document", s.Deploy.Kubeconfig)
	}
	vault, item, file := m[1], m[2], m[3]
	if _, err := d.LookPath("op"); err != nil {
		return fmt.Errorf("the 1Password CLI (op) is not on PATH; storing the kubeconfig at %s needs it", s.Deploy.Kubeconfig)
	}
	body, err := os.ReadFile(filepath.Join(d.Root, filepath.FromSlash(IdentityFile)))
	if err != nil {
		return fmt.Errorf("reading the rollout identity: %w", err)
	}
	id, err := readIdentity(body)
	if err != nil {
		return err
	}

	// Each read keeps its result in memory; none is printed.
	admin := func(args ...string) (string, error) {
		out, err := d.Exec(nil, false, "kubectl", append([]string{"--context", kubeContext}, args...)...)
		if err != nil {
			return "", fmt.Errorf("kubectl %s: %w", args[0], err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	server, err := admin("config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")
	if err != nil {
		return err
	}
	if server == "" {
		return fmt.Errorf("context %s names no cluster server", kubeContext)
	}
	field := func(name, path string) (string, error) {
		v, err := admin("-n", id.namespace, "get", "secret", id.secret, "-o", "jsonpath={.data."+path+"}")
		if err != nil {
			return "", fmt.Errorf("reading %s of secret %s/%s: %w", name, id.namespace, id.secret, err)
		}
		if v == "" {
			return "", fmt.Errorf("secret %s/%s has no %s yet; the token controller fills it in a moment after the Secret is applied", id.namespace, id.secret, name)
		}
		return v, nil
	}
	caB64, err := field("ca.crt", `ca\.crt`)
	if err != nil {
		return err
	}
	tokB64, err := field("token", "token")
	if err != nil {
		return err
	}
	ca, err := base64.StdEncoding.DecodeString(caB64)
	if err != nil {
		return fmt.Errorf("secret %s/%s: ca.crt is not base64: %w", id.namespace, id.secret, err)
	}
	if blk, _ := pem.Decode(ca); blk == nil {
		return fmt.Errorf("secret %s/%s: ca.crt holds no certificate", id.namespace, id.secret)
	}
	tok, err := base64.StdEncoding.DecodeString(tokB64)
	if err != nil {
		return fmt.Errorf("secret %s/%s: token is not base64: %w", id.namespace, id.secret, err)
	}
	cfg, err := render(id, server, caB64, strings.TrimSpace(string(tok)))
	if err != nil {
		return fmt.Errorf("writing the kubeconfig: %w", err)
	}

	dir, err := os.MkdirTemp("", "lateregate-credential")
	if err != nil {
		return fmt.Errorf("making a directory for the kubeconfig: %w", err)
	}
	sec := &secrets{dir: dir}
	defer d.guard(sec)()
	defer func() {
		if rmErr := sec.remove(); rmErr != nil && err == nil {
			err = fmt.Errorf("removing %s: %w", dir, rmErr)
		}
	}()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		return fmt.Errorf("writing the kubeconfig: %w", err)
	}

	// The Role must hold every kind deploy/prod applies; a server-side
	// dry run proves it before the first deploy does.
	flag := "-f"
	if _, err := os.Stat(filepath.Join(d.Root, "deploy", "prod", "kustomization.yaml")); err == nil {
		flag = "-k"
	}
	env := withEnv(d.Environ(), "KUBECONFIG="+path)
	if _, err := d.Exec(env, true, "kubectl", "--kubeconfig", path, "apply", "--dry-run=server", flag, "deploy/prod/"); err != nil {
		return fmt.Errorf("the identity %s cannot apply deploy/prod (a kind its Role lacks shows above): %w", id.account, err)
	}

	probe, err := combined(d.Exec, nil, "op", "item", "get", item, "--vault", vault)
	switch {
	case err == nil:
		if out, err := combined(d.Exec, nil, "op", "document", "edit", item, path, "--vault", vault, "--file-name", file); err != nil {
			return fmt.Errorf("`op document edit` of %s: %w: %s", item, err, firstLine(string(out)))
		}
	case missingItem.Match(probe):
		if out, err := combined(d.Exec, nil, "op", "document", "create", path, "--vault", vault, "--title", item, "--file-name", file); err != nil {
			return fmt.Errorf("`op document create` of %s: %w: %s", item, err, firstLine(string(out)))
		}
	default:
		return fmt.Errorf("`op item get %s` in vault %s: %w: %s", item, vault, err, firstLine(string(probe)))
	}

	back := filepath.Join(dir, "readback")
	if err := d.readKubeconfig(s.Deploy.Kubeconfig, back); err != nil {
		return fmt.Errorf("stored the Document, but reading it back failed: %w", err)
	}
	got, err := os.ReadFile(back)
	if err != nil {
		return fmt.Errorf("reading back the stored kubeconfig: %w", err)
	}
	if !bytes.Equal(got, cfg) {
		return fmt.Errorf("%s reads back different from what was stored: check that the reference names the Document just written", s.Deploy.Kubeconfig)
	}
	d.printf("stored the kubeconfig of %s (namespace %s) at %s\n", id.account, id.namespace, s.Deploy.Kubeconfig)
	return nil
}

var missingItem = regexp.MustCompile(`(?i)isn't an item|is not an item|not found|no item`)
