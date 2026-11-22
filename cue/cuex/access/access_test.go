/*
Copyright 2026 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package access_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubevela/pkg/apis/cue/v1alpha1"
	"github.com/kubevela/pkg/cue/cuex"
	"github.com/kubevela/pkg/cue/cuex/access"
	cuexruntime "github.com/kubevela/pkg/cue/cuex/runtime"
)

const system = "vela-system"

// rbac is an Authorizer answering from fixed bindings and grants, each where it
// was made.
type rbac struct {
	// members[namespace][role] are the users and groups bound to it there; ""
	// is a ClusterRoleBinding.
	members map[string]map[string][]string
	// grants[namespace] are subject|name pairs holding "call".
	grants map[string][]string
	labels map[string]map[string]string
	asked  []string
}

func subjects(w cuex.Writer) []string { return append([]string{w.User}, w.Groups...) }

func (r *rbac) Member(_ context.Context, w cuex.Writer, roles []v1alpha1.RoleRef, ns string) (bool, error) {
	for _, role := range roles {
		for _, where := range []string{"", ns} {
			for _, s := range subjects(w) {
				if slices.Contains(r.members[where][role.String()], s) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (r *rbac) MayCall(_ context.Context, w cuex.Writer, ns string, names []string) (bool, error) {
	r.asked = append(r.asked, ns+" "+strings.Join(names, ","))
	for _, s := range subjects(w) {
		for _, n := range names {
			if slices.Contains(r.grants[ns], s+"|"+n) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (r *rbac) NamespaceLabels(_ context.Context, ns string) (map[string]string, error) {
	return r.labels[ns], nil
}

// github is githubwrite's provider: every function answers with its #do and the
// title it was given, so a render shows the call ran.
type github struct {
	cuexruntime.Package
	calls *atomic.Int32
}

type ghParams struct {
	Params struct {
		Title string `json:"title"`
	} `json:"$params"`
}
type ghReturns struct {
	Returns struct {
		Done string `json:"done"`
	} `json:"$returns"`
}

func (g github) GetProviderFn(do string) cuexruntime.ProviderFn {
	return cuexruntime.Concurrent(cuexruntime.GenericProviderFn[ghParams, ghReturns](
		func(_ context.Context, p *ghParams) (*ghReturns, error) {
			g.calls.Add(1)
			r := &ghReturns{}
			r.Returns.Done = do + ":" + p.Params.Title
			return r, nil
		}))
}

func (g github) Source() *v1alpha1.Package { return g.Package.(cuexruntime.FromPackage).Source() }

func (g github) GroupsOf(do string) []string { return g.Package.(cuexruntime.FromPackage).GroupsOf(do) }

func function(def, do string, groups ...string) string {
	g := ""
	if len(groups) > 0 {
		quoted := make([]string, len(groups))
		for i, gr := range groups {
			quoted[i] = fmt.Sprintf("%q", gr)
		}
		g = fmt.Sprintf("\t$groups: [%s]\n", strings.Join(quoted, ", "))
	}
	return fmt.Sprintf("%s: {\n\t#do: %q\n\t#provider: \"githubwrite\"\n%s\t#config: maxPerRender: 64\n\t$params: title?: string\n\t$returns?: done: string\n}\n", def, do, g)
}

func pkg(ns, name, path, writer, source string, r *v1alpha1.PackageRestrictions) *v1alpha1.Package {
	p := &v1alpha1.Package{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: v1alpha1.PackageSpec{Path: path, Templates: map[string]string{"main.cue": source}, Restrictions: r}}
	if writer != "" {
		p.Annotations = map[string]string{v1alpha1.AnnotationWriter: writer}
	}
	return p
}

const sandwrap = `package sandwrap
import "example.com/githubwrite"
#Delete: {
	t: githubwrite.#ReposDelete & {$params: title: "x"}
	out: t.$returns.done
}
`

const platformBot = "system:serviceaccount:argocd:argocd-platform"

// fixture is githubwrite restricted in vela-system, a platform wrapper of it
// beside it, and two teams' wrappers of it in sandbox.
func fixture(t *testing.T) (*cuex.Compiler, *rbac, *atomic.Int32) {
	t.Helper()
	restrictions := &v1alpha1.PackageRestrictions{
		CallRule: v1alpha1.CallRule{Namespaces: []string{system, "team-*", "sandbox"}},
		Functions: []v1alpha1.FunctionRestriction{
			{Match: []string{"group:administration"}, CallRule: v1alpha1.CallRule{RequireRoles: []v1alpha1.RoleRef{{Kind: "ClusterRole", Name: "github-admins"}}}},
			{Match: []string{"group:issues"}, CallRule: v1alpha1.CallRule{RequireGrant: true}},
			{Match: []string{"pulls/merge"}, CallRule: v1alpha1.CallRule{Namespaces: []string{"platform-ops"}}},
			{Match: []string{"group:pulls"}, CallRule: v1alpha1.CallRule{RequireGrant: true}},
			{Match: []string{"group:checks", "group:statuses"}, CallRule: v1alpha1.CallRule{RequireGrant: true}},
			{Match: []string{"group:contents"}, CallRule: v1alpha1.CallRule{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"github-write": "true"}}}},
		},
	}
	source := "package githubwrite\n" +
		function("#ReposGet", "repos/get") +
		function("#ReposDelete", "repos/delete", "administration") +
		function("#IssuesCreate", "issues/create", "issues") +
		function("#PullsMerge", "pulls/merge", "pulls") +
		function("#ReposFork", "repos/fork", "contents") +
		function("#IssuesLinkPull", "issues/link-pull", "issues", "pulls") +
		function("#ChecksReport", "checks/report", "checks", "statuses")
	objects := []*v1alpha1.Package{
		pkg(system, "githubwrite", "example.com/githubwrite", "platform", source, restrictions),
		pkg(system, "notify", "example.com/notify", platformBot, `package notify
import "example.com/githubwrite"
#Send: {
	title: string
	t: githubwrite.#IssuesCreate & {$params: "title": title}
	out: t.$returns.done
}
`, nil),
		pkg("sandbox", "sandwrap", "acme.com/sandwrap", "alice", sandwrap, nil),
		pkg("sandbox", "bobwrap", "acme.com/bobwrap", "bob", strings.ReplaceAll(sandwrap, "sandwrap", "bobwrap"), nil),
	}
	c := cuex.NewCompilerWithInternalPackages()
	calls := &atomic.Int32{}
	byName := map[string]*v1alpha1.Package{}
	var gh *build.Instance
	var wrappers []*build.Instance
	for _, o := range objects {
		p, err := cuexruntime.NewExternalPackage(o)
		require.NoError(t, err)
		if o.Name == "githubwrite" {
			gh = p.GetImports()[0]
			p = github{Package: p, calls: calls}
		} else {
			wrappers = append(wrappers, p.GetImports()...)
		}
		c.Externals.Set("external://"+o.Namespace+"/"+o.Name, p)
		byName[o.Namespace+"/"+o.Name] = o
	}
	// A Package importing another arrives with dependencies; until then the
	// wrappers' import of githubwrite is wired here.
	for _, w := range wrappers {
		w.Imports = append(w.Imports, gh)
	}
	r := &rbac{
		members: map[string]map[string][]string{
			"":       {"ClusterRole/github-admins": {"alice"}},
			system:   {"ClusterRole/github-admins": {"carol"}},
			"team-a": {"ClusterRole/github-admins": {"bob"}},
		},
		grants: map[string][]string{system: {
			"payments-leads|example.com/githubwrite#group:issues",
			platformBot + "|example.com/githubwrite#group:issues",
			"pr-bots|example.com/githubwrite#group:pulls",
			"ci-bots|example.com/githubwrite#group:checks",
			"status-bots|example.com/githubwrite#group:statuses",
		}},
		labels: map[string]map[string]string{"team-a": {"github-write": "true"}, "team-c": {}},
	}
	c.CallCheck = (&access.Checker{
		Authorizer: r,
		Packages: func(ns, name string) (*v1alpha1.Package, bool) {
			p, ok := byName[ns+"/"+name]
			return p, ok
		},
	}).Check
	return c, r, calls
}

func direct(def, title string) string {
	return fmt.Sprintf("import \"example.com/githubwrite\"\ncall: githubwrite.%s & {$params: title: %q}\nout: call.$returns.done\n", def, title)
}

func owner(ns string, w *cuex.Writer) context.Context {
	return cuex.WithOwner(context.Background(), cuex.Owner{Kind: "ComponentDefinition", Namespace: ns, Name: "web", Writer: w})
}

func render(c *cuex.Compiler, ctx context.Context, src string) (string, error) {
	v, err := c.CompileString(ctx, src)
	if err != nil {
		return "", err
	}
	return v.LookupPath(cue.ParsePath("out")).String()
}

func TestCheck(t *testing.T) {
	alice := &cuex.Writer{User: "alice"}
	bob := &cuex.Writer{User: "bob"}
	carol := &cuex.Writer{User: "carol"}
	erin := &cuex.Writer{User: "erin"}
	lead := &cuex.Writer{User: "erin", Groups: []string{"payments-leads"}}
	c, _, _ := fixture(t)

	for _, tc := range []struct {
		name string
		ctx  context.Context
		src  string
		want string // the output, or "!" and part of the refusal
	}{
		{"namespace allowed, no entry", owner("team-a", erin), direct("#ReposGet", "a"), "repos/get:a"},
		{"namespace not allowed", owner("other", erin), direct("#ReposGet", "a"), "!ComponentDefinition other/web may not call example.com/githubwrite#repos/get: it is restricted"},

		{"role by ClusterRoleBinding", owner("team-a", alice), direct("#ReposDelete", "a"), "repos/delete:a"},
		{"role bound where the Package lives", owner("team-a", carol), direct("#ReposDelete", "a"), "repos/delete:a"},
		{"role bound in the team's namespace does not count", owner("team-a", bob), direct("#ReposDelete", "a"), "!its writer bob must be a member of ClusterRole/github-admins"},
		{"role and no writer", owner("team-a", nil), direct("#ReposDelete", "a"), "!it requires a role and there is no writer to check"},
		{"no owner at all", context.Background(), direct("#ReposDelete", "a"), "!a compilation with no owner may not call example.com/githubwrite#repos/delete"},

		{"grant by group", owner("team-a", lead), direct("#IssuesCreate", "a"), "issues/create:a"},
		{"no grant", owner("team-a", bob), direct("#IssuesCreate", "a"),
			`!its writer bob needs call on "example.com/githubwrite", "example.com/githubwrite#group:issues", "example.com/githubwrite#issues/create" in vela-system`},
		{"two groups, a grant for one", owner("team-a", lead), direct("#IssuesLinkPull", "a"),
			`!its writer erin needs call on "example.com/githubwrite", "example.com/githubwrite#group:pulls", "example.com/githubwrite#issues/link-pull" in vela-system`},
		{"two groups, a grant for each", owner("team-a", &cuex.Writer{User: "erin", Groups: []string{"payments-leads", "pr-bots"}}), direct("#IssuesLinkPull", "a"), "issues/link-pull:a"},
		{"one entry for two groups, a grant for one", owner("team-a", &cuex.Writer{User: "ci", Groups: []string{"ci-bots"}}), direct("#ChecksReport", "a"),
			`!its writer ci needs call on "example.com/githubwrite", "example.com/githubwrite#group:statuses", "example.com/githubwrite#checks/report" in vela-system`},
		{"one entry for two groups, a grant for each", owner("team-a", &cuex.Writer{User: "ci", Groups: []string{"ci-bots", "status-bots"}}), direct("#ChecksReport", "a"), "checks/report:a"},
		{"a function entry narrows namespaces", owner("team-a", alice), direct("#PullsMerge", "a"), "!may not call example.com/githubwrite#pulls/merge: it is restricted"},
		{"namespace selector matches", owner("team-a", erin), direct("#ReposFork", "a"), "repos/fork:a"},
		{"namespace selector does not", owner("team-c", erin), direct("#ReposFork", "a"), "!team-c/web may not call example.com/githubwrite#repos/fork: it is restricted"},

		{"a platform wrapper calls on its own writer's grant", owner("team-a", erin),
			"import \"example.com/notify\"\nn: notify.#Send & {title: \"a\"}\nout: n.out\n", "issues/create:a"},
		{"a hand-built call beside the wrapper is the template's", owner("team-a", erin),
			"import \"example.com/notify\"\nn: notify.#Send & {title: \"a\"}\nraw: {#do: \"issues/create\", #provider: \"githubwrite\", $params: title: \"b\"}\nout: raw.$returns.done\n",
			"!ComponentDefinition team-a/web may not call example.com/githubwrite#issues/create: its writer erin needs call"},
		{"reaching into the wrapper's call makes it the template's too", owner("team-a", erin),
			"import \"example.com/notify\"\nn: notify.#Send & {title: \"a\", t: $params: title: \"a\"}\nout: n.out\n",
			"!ComponentDefinition team-a/web may not call example.com/githubwrite#issues/create"},
		{"a team's wrapper is checked as its writer", owner("sandbox", erin),
			"import \"acme.com/sandwrap\"\nn: sandwrap.#Delete\nout: n.out\n", "repos/delete:x"},
		{"a team's wrapper whose writer lacks the role", owner("sandbox", alice),
			"import \"acme.com/bobwrap\"\nn: bobwrap.#Delete\nout: n.out\n",
			"!Package sandbox/bobwrap may not call example.com/githubwrite#repos/delete: its writer bob must be a member"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := render(c, tc.ctx, tc.src)
			if strings.HasPrefix(tc.want, "!") {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.want[1:])
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
}

// Calls run together are checked before any of them reaches the provider.
func TestCheckTogether(t *testing.T) {
	c, r, calls := fixture(t)
	src := "import \"example.com/githubwrite\"\nout: {\n" +
		"\ta: githubwrite.#IssuesCreate & {$params: title: \"a\"}\n" +
		"\tb: githubwrite.#ReposDelete & {$params: title: \"b\"}\n" +
		"} @concurrency(4)\n"
	_, err := render(c, owner("team-a", &cuex.Writer{User: "erin", Groups: []string{"payments-leads"}}), src)
	require.ErrorContains(t, err, "must be a member of ClusterRole/github-admins")
	require.Zero(t, calls.Load())
	require.NotEmpty(t, r.asked)
}
