// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

import (
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// verifierPackage verifies a token for every repository of the family.
const verifierPackage = "latere.ai/x/pkg/authkit/jwt"

// authorizerPackage carries the one authorizer contract a core asks over.
const authorizerPackage = "latere.ai/x/pkg/authz"

// sharedModule is the module the envelope is declared in. The repository
// that is that module declares the shape; every other repository sends and
// decodes the declaration rather than writing the fields again.
const sharedModule = "latere.ai/x/pkg"

// envelopeAsk is the question the authorizer contract carries: what is
// being done, by whom, and to what. A type that names the action beside
// either of the other two is the envelope written out again; the action
// alone is a discriminator, which a repository names for whatever it
// dispatches on, so it is not a finding by itself.
var envelopeAsk = []string{"subject", "resource"}

// envelopeAnswer is the decision the contract answers with: whether it is
// allowed, how long that holds, and why. A page is not a verdict and
// carries no ttl, so all three are required together.
var envelopeAnswer = []string{"allow", "ttl", "reason"}

// actionTag is the tag every ask carries.
const actionTag = "action"

// otherTokenLibraries are the libraries that would be a second verifier.
// The dependency gate's allow list carries the same decision.
var otherTokenLibraries = []string{
	"github.com/golang-jwt/jwt",
	"github.com/go-jose",
	"gopkg.in/square/go-jose",
	"github.com/lestrrat-go/jwx",
	"github.com/ory/fosite",
}

// claimIdentifiers and claimStrings are the membership claims a core may not
// read for meaning. It forwards every claim to the authorizer instead.
var claimIdentifiers = []string{"OrgID", "Roles", "IsSuperadmin", "PrincipalType"}

var claimStrings = []string{"org_id", "roles", "is_superadmin", "principal_type"}

// issuerPaths are the issuer endpoints a request path may not call.
var issuerPaths = []string{"/tokeninfo", "/userinfo/permissions"}

// retiredMechanisms are the delegation names the family removed. Each is a
// plain substring, in a Go file or a document outside an archive.
var retiredMechanisms = []string{"grantor_id", "tokens/exchange", "RFC 8693", "actor: true"}

// retiredNames are the packages and the vocabulary of earlier generations of
// the system, which a live document may not describe.
var retiredNames = []string{"pkg/oidclogin", "pkg/jwtauth", "pkg/oidc/", "identity fabric", "delegated token"}

// flagNames are the flag that access was once decided by, in the three
// spellings the family writes it: the claim and the column, Go's exported
// field, and the camel case a browser gives the same field. Each is a plain
// case-sensitive substring.
var flagNames = []string{"is_superadmin", "IsSuperadmin", "isSuperadmin"}

// rolesOnlyKey is the line the history is searched for, so the rule cannot
// be turned off once it is on.
const rolesOnlyKey = "roles_only: true"

// delegationClaim matches a delegation claim as a JSON key or a struct tag,
// so the English word "act" in a sentence is not a finding.
var delegationClaim = regexp.MustCompile(`"(act|agent_id|actor_id)(,[^"]*)?"`)

// claimTag and claimField are what make a Go type a claims type: it carries
// the registered claims beside whatever else it has. An attribution column
// named agent_id in a store type is not a token claim, which is the
// difference this rule is built on.
var claimTag = regexp.MustCompile(`json:"(sub|aud|exp)(,[^"]*)?"`)

var claimField = []string{"Sub", "Aud", "Exp"}

// claimsWord marks a file that is about the claims of a token whatever its
// types look like, including a document.
var claimsWord = regexp.MustCompile(`\bClaims\b`)

// word matches a name with a boundary on each side, so org_id inside a
// longer identifier is not a claim being read.
func word(name string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
}

var claimStringRe = compileWords(claimStrings)

func compileWords(names []string) map[string]*regexp.Regexp {
	out := make(map[string]*regexp.Regexp, len(names))
	for _, n := range names {
		out[n] = word(n)
	}
	return out
}

// ruleClaims holds a core to forwarding claims rather than reading them.
func ruleClaims(t *tree) (result, error) {
	files := t.claiming()
	if len(files) == 0 {
		return result{skip: "no non-test Go file outside the claims passthrough"}, nil
	}
	var found []Finding
	for _, g := range files {
		inspect(g.file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if slices.Contains(claimIdentifiers, x.Name) {
					found = append(found, at(g.rel, t.line(x.Pos()), claimSentence, x.Name))
				}
			case *ast.BasicLit:
				if x.Kind != token.STRING {
					return true
				}
				text, ok := literalText(x)
				if !ok {
					return true
				}
				for _, c := range claimStrings {
					if claimStringRe[c].MatchString(text) {
						found = append(found, at(g.rel, t.line(x.Pos()), claimSentence, c))
					}
				}
			}
			return true
		})
	}
	return result{findings: found,
		note: fmt.Sprintf("%d Go file(s) outside the passthrough name no claim", len(files))}, nil
}

const claimSentence = "this reads the membership claim %q for meaning; a core forwards every claim " +
	"to the authorizer and decides from none of them"

// ruleVerifier holds a repository to one token verifier.
func ruleVerifier(t *tree) (result, error) {
	if len(t.goFiles) == 0 {
		return result{skip: "no non-test Go file to read"}, nil
	}
	var found []Finding
	verifier := false
	for _, g := range t.goFiles {
		if g.imported(verifierPackage) {
			verifier = true
		}
		for _, imp := range g.file.Imports {
			ip, ok := literalText(imp.Path)
			if !ok {
				continue
			}
			for _, other := range otherTokenLibraries {
				if strings.HasPrefix(ip, other) {
					found = append(found, at(g.rel, t.line(imp.Pos()), secondLibrarySentence))
				}
			}
		}
		found = append(found, handRolledDecode(t, g)...)
	}
	// A bff forwards the person's token and verifies nothing itself, so the
	// shared verifier is not required of it; the other two halves still are.
	if !verifier && t.cfg.Role != config.RoleBFF {
		found = append(found, at("go.mod", 1, noVerifierSentence))
	}
	return result{findings: found,
		note: fmt.Sprintf("one verifier across %d Go file(s)", len(t.goFiles))}, nil
}

const secondLibrarySentence = "this imports a second token library; the family verifies with one " +
	"shared package, and the leaf id-04 is where a repository moves onto it"

const noVerifierSentence = "nothing here imports the shared token verifier, so this repository " +
	"verifies with something of its own; the leaf id-04 is where it moves onto the shared one"

const handRolledSentence = "this takes a token apart by hand, decoding one segment and splitting " +
	"on the separator; the shared verifier is what reads a token, so call it instead"

// handRolledDecode is a heuristic, and the report says so: a file that both
// decodes unpadded base64 and splits a string on the separator is taking a
// token apart, and a file doing that outside the shared package is a second
// verifier whatever it imports. Neither half alone is evidence.
func handRolledDecode(t *tree, g goFile) []Finding {
	if inDir(g.rel, "authkit") {
		return nil
	}
	var decodes []token.Pos
	splits := false
	ast.Inspect(g.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if selects(sel, "base64", "RawURLEncoding", "DecodeString") {
			decodes = append(decodes, sel.Pos())
		}
		if inner, ok := sel.X.(*ast.Ident); ok && inner.Name == "strings" &&
			slices.Contains([]string{"Split", "SplitN", "Cut", "Index", "LastIndex"}, sel.Sel.Name) {
			for _, lit := range call.Args {
				if b, ok := lit.(*ast.BasicLit); ok && b.Kind == token.STRING {
					if text, ok := literalText(b); ok && text == "." {
						splits = true
					}
				}
			}
		}
		return true
	})
	if !splits {
		return nil
	}
	out := make([]Finding, 0, len(decodes))
	for _, pos := range decodes {
		out = append(out, at(g.rel, t.line(pos), handRolledSentence))
	}
	return out
}

// selects reports whether a selector is pkg.Middle.Name.
func selects(sel *ast.SelectorExpr, pkg, middle, name string) bool {
	if sel.Sel.Name != name {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	if !ok || inner.Sel.Name != middle {
		return false
	}
	base, ok := inner.X.(*ast.Ident)
	return ok && base.Name == pkg
}

// ruleAuthorizer holds a core to one authorizer contract.
func ruleAuthorizer(t *tree) (result, error) {
	if len(t.goFiles) == 0 {
		return result{skip: "no non-test Go file to read"}, nil
	}
	var found []Finding
	client := false
	for _, g := range t.goFiles {
		if g.imported(authorizerPackage) {
			client = true
			continue
		}
		if !posts(g) {
			continue
		}
		for _, lit := range stringLiterals(g.file) {
			text, ok := literalText(lit)
			if !ok || !strings.Contains(text, "authorize") || !strings.Contains(text, "/") {
				continue
			}
			found = append(found, at(g.rel, t.line(lit.Pos()), handRolledAskSentence))
		}
	}
	if !client {
		found = append(found, at("go.mod", 1, noContractSentence))
	}
	return result{findings: found,
		note: fmt.Sprintf("one authorizer contract across %d Go file(s)", len(t.goFiles))}, nil
}

const noContractSentence = "nothing here imports the shared authorizer client, so this core asks " +
	"its question in a shape of its own; the leaf id-03 is where the shared contract ships"

const handRolledAskSentence = "this posts to an authorizer by hand; the shared client carries the " +
	"envelope, the cache rules and the failure rules, and the leaf id-03 is where it ships"

// ruleEnvelope holds the envelope and its decision to one declaration.
//
// The authorizer rule catches a repository that asks in a shape of its own.
// This one catches a repository that answers in one, or that decodes the
// answer into fields it wrote itself: either way the wire shape is declared
// twice, and the second copy stops matching the first on the day the first
// changes. The evidence is the JSON tags of a struct, because a type that
// marshals the envelope is what puts the shape on the wire; a Go field with
// no tag names nothing a reader of the wire sees.
//
// Tag names are matched whole. An actions list, an allowed-hosts set, a
// ttl in seconds and a reasons array share letters with the envelope and
// are not it.
func ruleEnvelope(t *tree) (result, error) {
	if t.shared() {
		return result{skip: "this is the module the envelope is declared in"}, nil
	}
	files := t.declaring()
	if len(files) == 0 {
		return result{skip: "no non-test Go file outside the declared envelope exemptions"}, nil
	}
	var found []Finding
	for _, g := range files {
		inspect(g.file, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			tags := jsonTags(st)
			switch {
			case tags[actionTag] && slices.ContainsFunc(envelopeAsk, func(name string) bool { return tags[name] }):
				found = append(found, at(g.rel, t.line(st.Pos()), envelopeAskSentence))
			case holdsAll(tags, envelopeAnswer):
				found = append(found, at(g.rel, t.line(st.Pos()), envelopeAnswerSentence))
			}
			return true
		})
	}
	return result{findings: found,
		note: fmt.Sprintf("%d Go file(s) declare no envelope of their own", len(files))}, nil
}

const envelopeAskSentence = "this type puts the authorizer's question on the wire in fields of its " +
	"own; one shared package declares that shape, so send and decode through it"

const envelopeAnswerSentence = "this type puts the authorizer's decision on the wire in fields of " +
	"its own; one shared package declares that shape, so send and decode through it"

// holdsAll reports whether every name is a tag of the type.
func holdsAll(tags map[string]bool, names []string) bool {
	for _, n := range names {
		if !tags[n] {
			return false
		}
	}
	return true
}

// jsonTags is the set of wire names one struct type marshals, read from the
// json tag of each field. An embedded field and a field with no tag name
// nothing on the wire of their own; a tag of "-" names nothing at all.
func jsonTags(st *ast.StructType) map[string]bool {
	out := map[string]bool{}
	for _, f := range st.Fields.List {
		if f.Tag == nil || len(f.Names) == 0 {
			continue
		}
		text, ok := literalText(f.Tag)
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(reflect.StructTag(text).Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

// posts reports whether a file builds a POST request.
func posts(g goFile) bool {
	if strings.Contains(g.text, "MethodPost") {
		return true
	}
	for _, lit := range stringLiterals(g.file) {
		if text, ok := literalText(lit); ok && text == "POST" {
			return true
		}
	}
	return false
}

// ruleRequestPath keeps the issuer off a request path.
func ruleRequestPath(t *tree) (result, error) {
	if len(t.goFiles) == 0 {
		return result{skip: "no non-test Go file to read"}, nil
	}
	var found []Finding
	read := 0
	for _, g := range t.goFiles {
		if t.frontend(g.rel) {
			continue
		}
		read++
		for _, lit := range stringLiterals(g.file) {
			text, ok := literalText(lit)
			if !ok {
				continue
			}
			hit := slices.ContainsFunc(issuerPaths, func(p string) bool { return strings.Contains(text, p) })
			if !hit && strings.Contains(text, "/orgs/") && strings.Contains(text, "/members") {
				hit = true
			}
			if hit {
				found = append(found, at(g.rel, t.line(lit.Pos()), requestPathSentence))
			}
		}
	}
	if read == 0 {
		return result{skip: "every non-test Go file is the frontend the block names"}, nil
	}
	return result{findings: found,
		note: fmt.Sprintf("%d Go file(s) call no issuer endpoint", read)}, nil
}

const requestPathSentence = "this calls the issuer while serving a request; a service verifies one " +
	"token, reads the membership it carries, and decides from its own state"

// ruleDelegation holds the one hop mechanism.
//
// The retired names are plain substrings anywhere. The claim names are not:
// one product carries an agent identity as an attribution column, which the
// family's decision allows, so those three are a finding only where they are
// a token claim: a struct tag in a type that also carries the registered
// claims, or a bare occurrence, outside any struct type, in a file that is
// about claims at all. A struct tag in a type that carries no registered
// claim is a column of that type, whatever the file imports.
func ruleDelegation(t *tree) (result, error) {
	if len(t.goFiles) == 0 && len(t.docs) == 0 {
		return result{skip: "no non-test Go file and no document outside the archive"}, nil
	}
	var found []Finding
	for _, g := range t.goFiles {
		found = append(found, retiredIn(g.sourceFile)...)
		claims, other := typeLines(t, g)
		found = append(found, delegationClaims(g.sourceFile, aboutClaims(g), claims, other)...)
	}
	for _, d := range t.docs {
		found = append(found, retiredIn(d)...)
		found = append(found, delegationClaims(d, claimsWord.MatchString(d.text), nil, nil)...)
	}
	return result{findings: found,
		note: fmt.Sprintf("%d Go file(s) and %d document(s) name no delegation", len(t.goFiles), len(t.docs))}, nil
}

const retiredSentence = "this names a delegation mechanism the family removed; one hop is a short " +
	"token minted for one audience, and no token carries a chain"

const delegationClaimSentence = "this carries a delegation claim the family removed; one hop is a " +
	"short token minted for one audience, and no token carries a chain"

func retiredIn(s sourceFile) []Finding {
	var found []Finding
	for i, line := range s.lines {
		for _, name := range retiredMechanisms {
			if strings.Contains(line, name) {
				found = append(found, at(s.rel, i+1, retiredSentence))
			}
		}
	}
	return found
}

// delegationClaims reports the claim names where they are a claim: on a line
// inside a type that carries the registered ones, or, in a file about
// claims, on a line outside every other struct type. A line inside a type
// that carries no registered claim is a column of that type.
func delegationClaims(s sourceFile, aboutClaims bool, inClaimsType, inOtherType map[int]bool) []Finding {
	var found []Finding
	for i, line := range s.lines {
		if !delegationClaim.MatchString(line) {
			continue
		}
		if inClaimsType[i+1] || (aboutClaims && !inOtherType[i+1]) {
			found = append(found, at(s.rel, i+1, delegationClaimSentence))
		}
	}
	return found
}

// aboutClaims reports whether a Go file is about the claims of a token.
func aboutClaims(g goFile) bool {
	return g.imported(verifierPackage) || claimsWord.MatchString(g.text)
}

// typeLines marks the lines of every struct type in the file: those of a
// type that carries the registered claims, where a delegation claim beside
// them is read as a claim, and those of every other type, where the same
// tag is a column of that type.
func typeLines(t *tree, g goFile) (claims, other map[int]bool) {
	claims, other = map[int]bool{}, map[int]bool{}
	ast.Inspect(g.file, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		into := other
		if carriesClaims(st) {
			into = claims
		}
		for line := t.line(st.Pos()); line <= t.line(st.End()); line++ {
			into[line] = true
		}
		return true
	})
	return claims, other
}

// carriesClaims reports whether a type holds the registered claims.
func carriesClaims(st *ast.StructType) bool {
	for _, f := range st.Fields.List {
		if f.Tag != nil && claimTag.MatchString(f.Tag.Value) {
			return true
		}
		for _, name := range f.Names {
			if slices.Contains(claimField, name.Name) {
				return true
			}
		}
	}
	return false
}

// ruleRoles holds access to roles rather than to a flag.
//
// The rule is one way. Once a tree has set the key, unsetting it is asked of
// the history rather than of the file, because a rule a repository can turn
// off is a rule that lasts until the first push that finds it inconvenient.
func ruleRoles(t *tree) (result, error) {
	if !t.cfg.RolesOnly {
		// Every ref, not the current branch: the key may have been set on a
		// branch that was merged, and a repository with no commit yet has no
		// history rather than an unreadable one, which asking HEAD cannot
		// tell apart from git being absent.
		out, err := t.exec(nil, false, "git", "log", "--all", "-S", rolesOnlyKey, "--format=%h", "--", config.Name)
		if err != nil {
			return result{}, fmt.Errorf("asking git whether this repository ever set %s in %s: %w\n"+
				"the rule is one way, so whether it was ever on is asked of the history; "+
				"this needs to run inside a git repository", rolesOnlyKey, config.Name, err)
		}
		if strings.TrimSpace(string(out)) != "" {
			return result{findings: []Finding{at(config.Name, 1, oneWaySentence)}}, nil
		}
		return result{skip: "the block does not set roles_only, and the history never did"}, nil
	}
	targets := t.everyFile()
	if len(targets) == 0 && len(t.frontendFiles) == 0 {
		return result{skip: "no non-test Go file, frontend source, document or manifest to read"}, nil
	}
	var found []Finding
	for _, s := range targets {
		found = append(found, flagHits(s, s.lines)...)
	}
	// A frontend keeps its sentences in the file rather than in a document
	// beside it, so the comment a repository writes about the flag it stopped
	// reading would be the finding. What decides is the code.
	for _, s := range t.frontendFiles {
		found = append(found, flagHits(s, code(s.lines))...)
	}
	return result{findings: found,
		note: fmt.Sprintf("%d file(s) decide by role and not by a flag", len(targets)+len(t.frontendFiles))}, nil
}

// flagHits reports every line of lines that names the flag. lines is read by
// index, so a caller that rewrites a line keeps its position.
func flagHits(s sourceFile, lines []string) []Finding {
	var found []Finding
	for i, line := range lines {
		for _, flag := range flagNames {
			if strings.Contains(line, flag) {
				found = append(found, at(s.rel, i+1, flagSentence))
			}
		}
	}
	return found
}

// code returns the lines with their comment spans blanked and everything else
// where it was, so a line's index is still its line number.
//
// String contents stay: a name read out of a template or quoted as a property
// is a decision. What goes is prose, which is where a file says why it no
// longer reads a name.
func code(lines []string) []string {
	out := make([]string, len(lines))
	var c commentScan
	for i, line := range lines {
		out[i] = c.code(line)
	}
	return out
}

// commentScan carries what one line leaves open for the next: a block comment,
// an HTML comment in a single-file component's template, or a template
// literal, each of which spans lines.
type commentScan struct{ block, html, template bool }

// code returns line without its comments and advances the scan.
//
// Quotes are tracked so a // inside a string is text and not a comment; a bare
// URL outside a string is the one shape this reads as a comment, and nothing
// after it on that line is read.
func (c *commentScan) code(line string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(line); i++ {
		switch {
		case c.block:
			if strings.HasPrefix(line[i:], "*/") {
				c.block = false
				i++
			}
			continue
		case c.html:
			if strings.HasPrefix(line[i:], "-->") {
				c.html = false
				i += 2
			}
			continue
		case c.template, quote != 0:
			if line[i] == '\\' {
				b.WriteByte(line[i])
				i++
				if i < len(line) {
					b.WriteByte(line[i])
				}
				continue
			}
			if (c.template && line[i] == '`') || line[i] == quote {
				c.template, quote = false, 0
			}
			b.WriteByte(line[i])
			continue
		}
		switch {
		case strings.HasPrefix(line[i:], "//"):
			return b.String()
		case strings.HasPrefix(line[i:], "/*"):
			c.block = true
			i++
		case strings.HasPrefix(line[i:], "<!--"):
			c.html = true
			i += 3
		case line[i] == '`':
			c.template = true
			b.WriteByte(line[i])
		case line[i] == '\'' || line[i] == '"':
			quote = line[i]
			b.WriteByte(line[i])
		default:
			b.WriteByte(line[i])
		}
	}
	return b.String()
}

const oneWaySentence = "the history of this file set roles_only and the file no longer does; the " +
	"rule is one way, so put the key back"

const flagSentence = "this names the flag the family replaced with a role; gate the route on a role " +
	"name from the standard five instead"

// everyFile is every scan target, for the rules that read all of them.
func (t *tree) everyFile() []sourceFile {
	out := make([]sourceFile, 0, len(t.goFiles)+len(t.docs)+len(t.manifests))
	for _, g := range t.goFiles {
		out = append(out, g.sourceFile)
	}
	out = append(out, t.docs...)
	for _, m := range t.manifests {
		out = append(out, m.sourceFile)
	}
	return out
}

// ruleNoCompanyValue keeps one company's deployment out of an open core.
//
// It reads code, manifests and user documents. A spec tree is the
// contributor's record, and a core extracted from a hosted deployment
// records that deployment's history and examples there, which a fork
// inherits as history and not as a default; so specs/ is not read.
//
// A declared overlay is the third thing it does not read: the block names
// the paths one company deploys its own installation from, and the values
// belong in them. A document may then name an address that overlay sets,
// because the sentence describes an installation the tree already holds
// rather than a default a fork would inherit. That address is the whole of
// the exemption, read out of the overlay: naming the overlay's path in a
// sentence admits nothing, and code and every manifest outside the overlay
// are held as before.
func ruleNoCompanyValue(t *tree) (result, error) {
	docs := map[string]bool{}
	for _, d := range t.docs {
		docs[d.rel] = true
	}
	var targets []sourceFile
	for _, s := range t.everyFile() {
		if strings.HasPrefix(s.rel, "specs/") || t.overlay(s.rel) {
			continue
		}
		targets = append(targets, s)
	}
	if len(targets) == 0 {
		return result{skip: "no non-test Go file, document or manifest outside specs/ to read"}, nil
	}
	imports := map[string]map[int]bool{}
	for _, g := range t.goFiles {
		lines := map[int]bool{}
		for _, imp := range g.file.Imports {
			for line := t.line(imp.Pos()); line <= t.line(imp.End()); line++ {
				lines[line] = true
			}
		}
		imports[g.rel] = lines
	}
	var found []Finding
	for _, s := range targets {
		for i, line := range s.lines {
			if imports[s.rel][i+1] {
				continue
			}
			// Only a document reads what the overlay carries: a default
			// compiled in or deployed from the base is a value whatever
			// sentence stands beside it.
			carried := func(string) bool { return false }
			if docs[s.rel] {
				carried = t.carries
			}
			if companyValue(line, t.cfg.APIGroup, carried) {
				found = append(found, at(s.rel, i+1, companySentence))
			}
		}
	}
	return result{findings: found,
		note: fmt.Sprintf("%d file(s) carry no value of one company", len(targets))}, nil
}

const companySentence = "this names one company's deployment; an open core anybody runs carries no " +
	"value of the company that hosts it, so move this to an overlay"

// companyNames are the two spellings of a hosted deployment: the public host
// and the cluster-internal one.
var companyNames = []string{"latere.ai", "latere.svc"}

// companyValue reports whether a line names one company outside the group
// the core writes its own manifests under and outside what carried admits.
func companyValue(line, group string, carried func(string) bool) bool {
	for _, name := range companyNames {
		for from := 0; ; {
			i := strings.Index(line[from:], name)
			if i < 0 {
				break
			}
			i += from
			from = i + len(name)
			if exempt(line, i, group) {
				continue
			}
			tok, _ := nameAt(line, i)
			if carried(host(tok)) {
				continue
			}
			return true
		}
	}
	return false
}

// hosts are the addresses of one company a line names, read as the
// no-company-value rule reads them, so an overlay is harvested with the
// walk the scan matches against.
func hosts(line string) []string {
	var out []string
	for _, name := range companyNames {
		for from := 0; ; {
			i := strings.Index(line[from:], name)
			if i < 0 {
				break
			}
			i += from
			from = i + len(name)
			tok, _ := nameAt(line, i)
			out = append(out, host(tok))
		}
	}
	return out
}

// nameAt is the name an occurrence sits in: the run of name bytes around it,
// carrying the path that follows it, and where that run starts.
func nameAt(line string, i int) (string, int) {
	start := i
	for start > 0 && isNameByte(line[start-1]) {
		start--
	}
	end := i
	for end < len(line) && (isNameByte(line[end]) || line[end] == '/') {
		end++
	}
	return line[start:end], start
}

// host is a token without the path after it and without the punctuation a
// sentence ends on, which is the address a reader of the line sees and the
// address an overlay sets. Both sides of the overlay comparison read it, so
// a document that ends a sentence on an address still names that address.
func host(tok string) string {
	name, _, _ := strings.Cut(tok, "/")
	return strings.TrimRight(name, ".-")
}

// exempt reports whether an occurrence is the core's own API group, which it
// writes into its own manifests and documents. The group is declared in the
// block rather than guessed, and an occurrence reached through a URL is not
// the group even when it reads like one.
func exempt(line string, i int, group string) bool {
	tok, start := nameAt(line, i)
	// The module namespace and a contact address are the project's own
	// coordinates, which the cores' invariant names as not forbidden.
	if strings.HasPrefix(tok, modulePrefix) {
		return true
	}
	if start > 0 && line[start-1] == '@' {
		return true
	}
	if group == "" {
		return false
	}
	if start > 0 && line[start-1] == '/' {
		return false
	}
	return tok == group || strings.HasPrefix(tok, group+"/")
}

// modulePrefix is the namespace every module of the family lives under.
const modulePrefix = "latere.ai/x/"

func isNameByte(b byte) bool {
	return b == '.' || b == '-' || b == '_' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// ruleClientAudiences holds a client to one audience per product.
//
// The half this scan holds is that a product audience is named in one file,
// the one that mints for it. The other half, that a credential read from a
// store never reaches a header outside that file, is dataflow and is left to
// the client's own tests.
func ruleClientAudiences(t *tree) (result, error) {
	if len(t.cfg.Audiences) == 0 {
		return result{skip: "the block names no product audience"}, nil
	}
	if len(t.goFiles) == 0 {
		return result{skip: "no non-test Go file to read"}, nil
	}
	var found []Finding
	for _, audience := range t.cfg.Audiences {
		re := word(audience)
		places := map[string]int{}
		for _, g := range t.goFiles {
			for _, lit := range stringLiterals(g.file) {
				text, ok := literalText(lit)
				if !ok || !re.MatchString(text) {
					continue
				}
				if _, seen := places[g.rel]; !seen {
					places[g.rel] = t.line(lit.Pos())
				}
			}
		}
		switch {
		case len(places) == 0:
			found = append(found, at(config.Name, 1, unmintedSentence, audience))
		case len(places) > 1:
			for rel, line := range places {
				found = append(found, at(rel, line, twoMintersSentence, audience))
			}
		}
	}
	return result{findings: found,
		note: fmt.Sprintf("%d product audience(s), each presented from one file", len(t.cfg.Audiences))}, nil
}

const unmintedSentence = "the block names the product audience %q and no file here presents it; " +
	"name what this repository really mints for, or delete the entry"

const twoMintersSentence = "a second file here also names the product audience %q; one file mints " +
	"for one product, so a credential cannot be presented to the wrong one"

// ruleDocuments keeps a live document describing the system that exists.
func ruleDocuments(t *tree) (result, error) {
	if len(t.docs) == 0 {
		return result{skip: "the tree has no document outside the archive"}, nil
	}
	var found []Finding
	for _, d := range t.docs {
		for i, line := range d.lines {
			for _, name := range retiredNames {
				if strings.Contains(line, name) {
					found = append(found, at(d.rel, i+1, staleSentence))
				}
			}
		}
	}
	return result{findings: found,
		note: fmt.Sprintf("%d document(s) describe the system that exists", len(t.docs))}, nil
}

const staleSentence = "this names a package or a mechanism of an earlier generation; describe the " +
	"one that exists, or move the file into the archive"
