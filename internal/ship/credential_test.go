// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const identityYAML = `apiVersion: v1
kind: Namespace
metadata: {name: latere}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: auth-rollout
  namespace: latere
---
apiVersion: v1
kind: Secret
type: kubernetes.io/service-account-token
metadata:
  name: auth-rollout-token
  namespace: latere
  annotations:
    kubernetes.io/service-account.name: auth-rollout
`

const testPEM = "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUZABC\n-----END CERTIFICATE-----\n"

const tokenValue = "tok-SECRET-VALUE"

// credRig is a rig whose repository holds a rollout identity and whose
// cluster and vault answer for it.
func credRig(t *testing.T) (*rig, *string) {
	t.Helper()
	r := newRig(t, "")
	write := func(rel, body string) {
		p := filepath.Join(r.d.Root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(IdentityFile, identityYAML)
	write("deploy/prod/deployment.yaml", "kind: Deployment\n")
	stored := new(string)
	r.answer = func(c call) ([]byte, error, bool) {
		switch c.name {
		case "kubectl":
			switch {
			case c.has("view"):
				return []byte("https://k8s.example:6443"), nil, true
			case c.has("secret") && slices.Contains(c.args, `jsonpath={.data.ca\.crt}`):
				return []byte(base64.StdEncoding.EncodeToString([]byte(testPEM))), nil, true
			case c.has("secret"):
				return []byte(base64.StdEncoding.EncodeToString([]byte(tokenValue))), nil, true
			}
		case "op":
			switch c.args[0] {
			case "item":
				return []byte("[ERROR] \"auth rollout kubeconfig\" isn't an item in the \"LatereAI\" vault.\n"), errors.New("exit 1"), true
			case "document":
				// create takes the file first, edit takes the item first.
				path := c.args[2]
				if c.args[1] == "edit" {
					path = c.args[3]
				}
				b, err := os.ReadFile(path)
				if err != nil {
					t.Error(err)
				}
				*stored = string(b)
				return nil, nil, true
			case "read":
				return nil, os.WriteFile(c.args[slices.Index(c.args, "--out-file")+1], []byte(*stored), 0o600), true
			}
		}
		return nil, nil, false
	}
	return r, stored
}

func TestDeployCredentialStoresTheIdentitysKubeconfig(t *testing.T) {
	r, stored := credRig(t)
	if err := DeployCredential(r.d, "admin"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"server: https://k8s.example:6443", "token: " + tokenValue, "namespace: latere", "current-context: auth-rollout", "certificate-authority-data: " + base64.StdEncoding.EncodeToString([]byte(testPEM))} {
		if !strings.Contains(*stored, want) {
			t.Errorf("kubeconfig lacks %q:\n%s", want, *stored)
		}
	}
	c, ok := r.find("op document create")
	if !ok {
		t.Fatalf("no create: %v", r.calls)
	}
	if c.args[3] != "--vault" || c.args[4] != "LatereAI" || !slices.Contains(c.args, "auth rollout kubeconfig") || c.args[len(c.args)-1] != "kubeconfig" {
		t.Errorf("create: %s", c)
	}
	// Every call against the admin's kubeconfig names the context; the check
	// of the new identity uses its own file.
	verified := false
	for _, c := range r.calls {
		if c.name != "kubectl" {
			continue
		}
		if c.has("--kubeconfig") {
			verified = true
			if !c.has("--dry-run=server") || !c.has("-f") || c.has("--context") || c.mode != 0o600 {
				t.Errorf("verify: %s (mode %v)", c, c.mode)
			}
			continue
		}
		if i := slices.Index(c.args, "--context"); i != 0 || c.args[1] != "admin" {
			t.Errorf("kubectl without --context admin: %s", c)
		}
	}
	if !verified {
		t.Error("the identity was not checked with a server-side dry run")
	}
	want := "stored the kubeconfig of auth-rollout (namespace latere) at " + testRef + "\n"
	if r.out.String() != want {
		t.Errorf("output %q, want %q", r.out.String(), want)
	}
	if strings.Contains(r.out.String(), tokenValue) {
		t.Error("the token was printed")
	}
	for _, c := range r.calls {
		if strings.Contains(c.String(), tokenValue) {
			t.Errorf("the token is on a command line: %s", c)
		}
	}
	if left := r.leftovers(); len(left) != 0 {
		t.Errorf("left %v", left)
	}
}

func TestDeployCredentialEditsAnExistingDocumentAndUsesKustomize(t *testing.T) {
	r, stored := credRig(t)
	inner := r.answer
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "op" && c.args[0] == "item" {
			return []byte("title: x\n"), nil, true
		}
		return inner(c)
	}
	if err := os.WriteFile(filepath.Join(r.d.Root, "deploy", "prod", "kustomization.yaml"), []byte("resources: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeployCredential(r.d, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.find("op document create"); ok {
		t.Error("created over an existing item")
	}
	e, ok := r.find("op document edit auth rollout kubeconfig")
	if !ok || e.args[len(e.args)-1] != "kubeconfig" {
		t.Errorf("edit: %v", r.calls)
	}
	v, _ := r.find("kubectl --kubeconfig")
	if !v.has("-k") || *stored == "" {
		t.Errorf("verify: %s", v)
	}
}

func TestDeployCredentialRefusals(t *testing.T) {
	t.Run("empty context", func(t *testing.T) {
		r, _ := credRig(t)
		for _, c := range []string{"", "  "} {
			if err := DeployCredential(r.d, c); err == nil || !strings.Contains(err.Error(), "-context is required") {
				t.Errorf("%q: %v", c, err)
			}
		}
		if len(r.calls) != 0 {
			t.Errorf("ran %v", r.calls)
		}
	})
	t.Run("no identity file", func(t *testing.T) {
		r, _ := credRig(t)
		if err := os.Remove(filepath.Join(r.d.Root, IdentityFile)); err != nil {
			t.Fatal(err)
		}
		if err := DeployCredential(r.d, "admin"); err == nil || !strings.Contains(err.Error(), "rollout identity") {
			t.Errorf("error: %v", err)
		}
	})
	t.Run("no service account", func(t *testing.T) {
		if _, err := readIdentity([]byte("kind: Namespace\nmetadata: {name: x}\n")); err == nil || !strings.Contains(err.Error(), "no ServiceAccount") {
			t.Errorf("error: %v", err)
		}
	})
	t.Run("no token secret", func(t *testing.T) {
		body := identityYAML[:strings.Index(identityYAML, "---\napiVersion: v1\nkind: Secret")]
		if _, err := readIdentity([]byte(body)); err == nil || !strings.Contains(err.Error(), "kubernetes.io/service-account-token") {
			t.Errorf("error: %v", err)
		}
		other := strings.Replace(identityYAML, "service-account.name: auth-rollout", "service-account.name: someone-else", 1)
		if _, err := readIdentity([]byte(other)); err == nil || !strings.Contains(err.Error(), "auth-rollout") {
			t.Errorf("a secret for another account: %v", err)
		}
	})
	t.Run("two accounts", func(t *testing.T) {
		two := identityYAML + "---\nkind: ServiceAccount\nmetadata: {name: b, namespace: latere}\n"
		if _, err := readIdentity([]byte(two)); err == nil || !strings.Contains(err.Error(), "2 ServiceAccounts") {
			t.Errorf("error: %v", err)
		}
	})
	t.Run("account without a namespace", func(t *testing.T) {
		if _, err := readIdentity([]byte("kind: ServiceAccount\nmetadata: {name: a}\n")); err == nil || !strings.Contains(err.Error(), "namespace") {
			t.Errorf("error: %v", err)
		}
	})
	t.Run("token not issued yet", func(t *testing.T) {
		r, _ := credRig(t)
		inner := r.answer
		r.answer = func(c call) ([]byte, error, bool) {
			if c.name == "kubectl" && c.has("secret") {
				return nil, nil, true
			}
			return inner(c)
		}
		if err := DeployCredential(r.d, "admin"); err == nil || !strings.Contains(err.Error(), "has no ca.crt yet") {
			t.Errorf("error: %v", err)
		}
	})
	t.Run("dry run fails", func(t *testing.T) {
		r, _ := credRig(t)
		inner := r.answer
		r.answer = func(c call) ([]byte, error, bool) {
			if c.name == "kubectl" && c.has("--dry-run=server") {
				return nil, errors.New("exit 1"), true
			}
			return inner(c)
		}
		err := DeployCredential(r.d, "admin")
		if err == nil || !strings.Contains(err.Error(), "cannot apply deploy/prod") {
			t.Fatalf("error: %v", err)
		}
		if _, ok := r.find("op document"); ok {
			t.Error("stored a credential that failed its check")
		}
		if left := r.leftovers(); len(left) != 0 {
			t.Errorf("left %v", left)
		}
	})
	t.Run("another probe failure is not a missing item", func(t *testing.T) {
		r, _ := credRig(t)
		inner := r.answer
		r.answer = func(c call) ([]byte, error, bool) {
			if c.name == "op" && c.args[0] == "item" {
				return []byte("[ERROR] you are not signed in\n"), errors.New("exit 1"), true
			}
			return inner(c)
		}
		if err := DeployCredential(r.d, "admin"); err == nil || !strings.Contains(err.Error(), "not signed in") {
			t.Fatalf("error: %v", err)
		}
		if _, ok := r.find("op document"); ok {
			t.Error("wrote a Document after an unreadable probe")
		}
	})
	t.Run("read back differs", func(t *testing.T) {
		r, _ := credRig(t)
		inner := r.answer
		r.answer = func(c call) ([]byte, error, bool) {
			if c.name == "op" && c.args[0] == "read" {
				return nil, os.WriteFile(c.args[slices.Index(c.args, "--out-file")+1], []byte("another document"), 0o600), true
			}
			return inner(c)
		}
		if err := DeployCredential(r.d, "admin"); err == nil || !strings.Contains(err.Error(), "reads back different") {
			t.Fatalf("error: %v", err)
		}
		if left := r.leftovers(); len(left) != 0 {
			t.Errorf("left %v", left)
		}
	})
	t.Run("op missing", func(t *testing.T) {
		r, _ := credRig(t)
		delete(r.have, "op")
		if err := DeployCredential(r.d, "admin"); err == nil || !strings.Contains(err.Error(), "op") {
			t.Fatalf("error: %v", err)
		}
	})
	t.Run("not a document reference", func(t *testing.T) {
		r, _ := credRig(t)
		r.d.Cfg.Ship.Deploy.Kubeconfig = "op://v/i/section/field"
		if err := DeployCredential(r.d, "admin"); err == nil || !strings.Contains(err.Error(), "op://VAULT/ITEM/FILE") {
			t.Fatalf("error: %v", err)
		}
	})
	t.Run("cluster has no server", func(t *testing.T) {
		r, _ := credRig(t)
		inner := r.answer
		r.answer = func(c call) ([]byte, error, bool) {
			if c.name == "kubectl" && c.has("view") {
				return []byte(" \n"), nil, true
			}
			return inner(c)
		}
		if err := DeployCredential(r.d, "admin"); err == nil || !strings.Contains(err.Error(), "no cluster server") {
			t.Fatalf("error: %v", err)
		}
	})
	t.Run("ca is not a certificate", func(t *testing.T) {
		r, _ := credRig(t)
		inner := r.answer
		r.answer = func(c call) ([]byte, error, bool) {
			if c.name == "kubectl" && slices.Contains(c.args, `jsonpath={.data.ca\.crt}`) {
				return []byte(base64.StdEncoding.EncodeToString([]byte("junk"))), nil, true
			}
			return inner(c)
		}
		if err := DeployCredential(r.d, "admin"); err == nil || !strings.Contains(err.Error(), "no certificate") {
			t.Fatalf("error: %v", err)
		}
	})
}
