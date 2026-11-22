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
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubevela/pkg/apis/cue/v1alpha1"
	"github.com/kubevela/pkg/cue/cuex/access"
)

func TestValidate(t *testing.T) {
	badSelector := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "tier", Operator: "Near"}}}
	for name, tc := range map[string]struct {
		r    *v1alpha1.PackageRestrictions
		want string
	}{
		"none": {nil, ""},
		"a rule that reads": {&v1alpha1.PackageRestrictions{
			CallRule: v1alpha1.CallRule{Namespaces: []string{"team-*"}, RequireGrant: true},
			Functions: []v1alpha1.FunctionRestriction{{Match: []string{"issues/create", "group:checks"},
				CallRule: v1alpha1.CallRule{RequireRoles: []v1alpha1.RoleRef{{Kind: "ClusterRole", Name: "leads"}}}}},
		}, ""},
		"a glob that cannot be read": {&v1alpha1.PackageRestrictions{CallRule: v1alpha1.CallRule{Namespaces: []string{"team-["}}},
			`spec.restrictions.namespaces[0]: "team-[" is not a namespace name or glob`},
		"a selector that cannot be read": {&v1alpha1.PackageRestrictions{Functions: []v1alpha1.FunctionRestriction{{CallRule: v1alpha1.CallRule{NamespaceSelector: badSelector}}}},
			`spec.restrictions.functions[0].namespaceSelector:`},
		"an empty match": {&v1alpha1.PackageRestrictions{Functions: []v1alpha1.FunctionRestriction{{Match: []string{""}}}},
			`spec.restrictions.functions[0].match[0]: empty; leave match out to take every function`},
		"an empty group": {&v1alpha1.PackageRestrictions{Functions: []v1alpha1.FunctionRestriction{{Match: []string{"group:"}}}},
			`spec.restrictions.functions[0].match[0]: "group:" names no group`},
		"a role with no name": {&v1alpha1.PackageRestrictions{CallRule: v1alpha1.CallRule{RequireRoles: []v1alpha1.RoleRef{{Kind: "Role"}}}},
			`spec.restrictions.requireRoles[0]: a role needs a name`},
	} {
		t.Run(name, func(t *testing.T) {
			err := access.Validate(tc.r)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}
