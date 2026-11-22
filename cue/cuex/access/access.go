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

// Package access decides whether a call to a Package's function may run, from
// the Package's restrictions, the caller's namespace, and the roles and grants
// of whoever wrote the call.
package access

import (
	"context"
	"fmt"
	"strings"

	"cuelang.org/go/cue"

	"github.com/kubevela/pkg/apis/cue/v1alpha1"
	"github.com/kubevela/pkg/cue/cuex"
	cuexruntime "github.com/kubevela/pkg/cue/cuex/runtime"
	"github.com/kubevela/pkg/util/nsmatch"
)

// SystemNamespace is the namespace a compilation with no owner is checked as.
const SystemNamespace = "vela-system"

// Authorizer answers what the check asks of the cluster.
type Authorizer interface {
	// Member reports whether a ClusterRoleBinding, or a RoleBinding in namespace,
	// binds one of roles to w's user or one of its groups.
	Member(ctx context.Context, w cuex.Writer, roles []v1alpha1.RoleRef, namespace string) (bool, error)
	// MayCall reports whether w holds the verb "call" on packages in cue.oam.dev
	// under any of names, in namespace.
	MayCall(ctx context.Context, w cuex.Writer, namespace string, names []string) (bool, error)
	// NamespaceLabels returns a namespace's labels, nil when it is not known.
	NamespaceLabels(ctx context.Context, namespace string) (map[string]string, error)
}

// Checker is a cuex.CallCheck for Packages with restrictions.
type Checker struct {
	Authorizer Authorizer
	// Packages finds a Package by namespace and name, for the writer of a call
	// written in its files.
	Packages func(namespace, name string) (*v1alpha1.Package, bool)
}

// caller is someone a call is checked as.
type caller struct {
	label     string
	namespace string
	writer    *cuex.Writer
}

// Check implements cuex.CallCheck. A call to a provider that is not a Package
// with restrictions is allowed. Otherwise every caller that wrote part of the
// call must pass: the template, and any other Package whose files did.
func (c *Checker) Check(ctx context.Context, provider cuexruntime.Provider, fn string, call cue.Value) error {
	target, ok := provider.(cuexruntime.FromPackage)
	if !ok || target.Source().Spec.Restrictions == nil {
		return nil
	}
	for _, who := range c.callers(ctx, call, target.Source()) {
		if err := c.allow(ctx, who, target, fn); err != nil {
			return err
		}
	}
	return nil
}

// callers are whoever wrote the call: each conjunct's file names a Package, or
// is the template's. A Package calling its own functions is no one to check.
func (c *Checker) callers(ctx context.Context, call cue.Value, target *v1alpha1.Package) []caller {
	op, parts := call.Expr()
	if op != cue.AndOp {
		parts = []cue.Value{call}
	}
	seen := map[string]bool{}
	var out []caller
	for _, part := range parts {
		var who caller
		if ns, name, ok := cuexruntime.PackageOfFile(part.Pos().Filename()); ok {
			if ns == target.Namespace && name == target.Name {
				continue
			}
			who = caller{label: "Package " + ns + "/" + name, namespace: ns}
			if p, found := c.Packages(ns, name); found {
				who.writer = WriterOf(p.Annotations)
			}
		} else {
			who = templateCaller(ctx)
		}
		if !seen[who.label] {
			seen[who.label] = true
			out = append(out, who)
		}
	}
	return out
}

func templateCaller(ctx context.Context) caller {
	owner, ok := cuex.OwnerFrom(ctx)
	if !ok {
		return caller{label: "a compilation with no owner", namespace: SystemNamespace}
	}
	return caller{label: owner.Kind + " " + owner.Namespace + "/" + owner.Name, namespace: owner.Namespace, writer: owner.Writer}
}

// WriterOf reads the writer stamp from an object's annotations, nil when it has
// none.
func WriterOf(annotations map[string]string) *cuex.Writer {
	user := annotations[v1alpha1.AnnotationWriter]
	if user == "" {
		return nil
	}
	w := &cuex.Writer{User: user}
	if groups := annotations[v1alpha1.AnnotationWriterGroups]; groups != "" {
		w.Groups = strings.Split(groups, ",")
	}
	return w
}

func (c *Checker) allow(ctx context.Context, who caller, target cuexruntime.FromPackage, fn string) error {
	src := target.Source()
	base, _, _ := strings.Cut(src.Spec.Path, "@")
	refuse := func(why string) error {
		return fmt.Errorf("%s may not call %s#%s: %s", who.label, base, fn, why)
	}
	for _, sel := range selectRules(src.Spec.Restrictions, fn, target.GroupsOf(fn)) {
		rule := sel.rule
		in, err := c.inNamespaces(ctx, rule, who.namespace)
		if err != nil {
			return refuse(err.Error())
		}
		if !in {
			return refuse("it is restricted")
		}
		if len(rule.RequireRoles) > 0 {
			if who.writer == nil {
				return refuse("it requires a role and there is no writer to check")
			}
			ok, err := c.Authorizer.Member(ctx, *who.writer, rule.RequireRoles, src.Namespace)
			if err != nil {
				return refuse(err.Error())
			}
			if !ok {
				roles := make([]string, len(rule.RequireRoles))
				for i, role := range rule.RequireRoles {
					roles[i] = role.String()
				}
				return refuse(fmt.Sprintf("its writer %s must be a member of %s", who.writer.User, strings.Join(roles, " or ")))
			}
		}
		if !rule.RequireGrant {
			continue
		}
		if who.writer == nil {
			return refuse("it requires a grant and there is no writer to check")
		}
		// a grant for each group the rule was selected for, as GitHub asks a
		// token for each permission an endpoint needs
		for _, g := range sel.groups {
			names := []string{base, base + "#" + fn}
			if g != "" {
				names = []string{base, base + "#group:" + g, base + "#" + fn}
			}
			ok, err := c.Authorizer.MayCall(ctx, *who.writer, src.Namespace, names)
			if err != nil {
				return refuse(err.Error())
			}
			if !ok {
				quoted := make([]string, len(names))
				for i, n := range names {
					quoted[i] = fmt.Sprintf("%q", n)
				}
				return refuse(fmt.Sprintf("its writer %s needs call on %s in %s", who.writer.User, strings.Join(quoted, ", "), src.Namespace))
			}
		}
	}
	return nil
}

func (c *Checker) inNamespaces(ctx context.Context, rule v1alpha1.CallRule, ns string) (bool, error) {
	var labels map[string]string
	if rule.NamespaceSelector != nil {
		var err error
		if labels, err = c.Authorizer.NamespaceLabels(ctx, ns); err != nil {
			return false, err
		}
	}
	return nsmatch.Matches(rule.Namespaces, rule.NamespaceSelector, ns, labels), nil
}

// selected is a rule that applies to a call, with the groups it applies for.
type selected struct {
	rule   v1alpha1.CallRule
	groups []string
}

// selectRules picks the package's rule, for every group, and for each group the
// first entry matching the function or that group. A function in no group takes
// the first entry matching its name. An entry selected for several groups is
// checked once, with all of them.
func selectRules(r *v1alpha1.PackageRestrictions, fn string, groups []string) []selected {
	all := groups
	if len(all) == 0 {
		all = []string{""}
	}
	out := []selected{{r.CallRule, all}}
	at := map[int]int{}
	for _, g := range all {
		for i, entry := range r.Functions {
			if !matches(entry.Match, fn, g) {
				continue
			}
			if j, ok := at[i]; ok {
				out[j].groups = append(out[j].groups, g)
			} else {
				at[i] = len(out)
				out = append(out, selected{entry.CallRule, []string{g}})
			}
			break
		}
	}
	return out
}

// matches reports whether an entry's match list takes fn or group. An empty
// list takes every function.
func matches(match []string, fn, group string) bool {
	if len(match) == 0 {
		return true
	}
	for _, m := range match {
		if m == fn || (group != "" && m == "group:"+group) {
			return true
		}
	}
	return false
}
