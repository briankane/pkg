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

package nsmatch_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubevela/pkg/util/nsmatch"
)

func TestMatches(t *testing.T) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "prod"}}
	prod := map[string]string{"tier": "prod"}
	for name, tc := range map[string]struct {
		names  []string
		sel    *metav1.LabelSelector
		ns     string
		labels map[string]string
		want   bool
	}{
		"nothing set matches every namespace": {ns: "team-a", want: true},
		"a name":                              {names: []string{"team-a"}, ns: "team-a", want: true},
		"a glob":                              {names: []string{"team-*"}, ns: "team-b", want: true},
		"no name matching":                    {names: []string{"team-*"}, ns: "ops", want: false},
		"an unparseable glob matches nothing": {names: []string{"team-["}, ns: "team-[", want: false},
		"a selector":                          {sel: sel, ns: "ops", labels: prod, want: true},
		"a selector, labels unknown":          {sel: sel, ns: "ops", want: false},
		"either is enough":                    {names: []string{"team-*"}, sel: sel, ns: "ops", labels: prod, want: true},
		"an invalid selector matches nothing": {sel: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "tier", Operator: "Bogus"}}}, ns: "ops", labels: prod, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, nsmatch.Matches(tc.names, tc.sel, tc.ns, tc.labels))
		})
	}
}
