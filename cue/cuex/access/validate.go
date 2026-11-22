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

package access

import (
	"errors"
	"fmt"
	"path"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubevela/pkg/apis/cue/v1alpha1"
)

// Validate reports what in a Package's restrictions cannot be read. A rule the
// check cannot read matches no caller, so it refuses every call; admission says
// why instead.
func Validate(r *v1alpha1.PackageRestrictions) error {
	if r == nil {
		return nil
	}
	const at = "spec.restrictions"
	errs := validateRule(at, r.CallRule)
	for i, entry := range r.Functions {
		field := fmt.Sprintf("%s.functions[%d]", at, i)
		for j, m := range entry.Match {
			switch {
			case m == "":
				errs = append(errs, fmt.Errorf("%s.match[%d]: empty; leave match out to take every function", field, j))
			case m == "group:":
				errs = append(errs, fmt.Errorf("%s.match[%d]: %q names no group", field, j, m))
			}
		}
		errs = append(errs, validateRule(field, entry.CallRule)...)
	}
	return errors.Join(errs...)
}

func validateRule(at string, rule v1alpha1.CallRule) []error {
	var errs []error
	for i, ns := range rule.Namespaces {
		if _, err := path.Match(ns, ""); err != nil || strings.TrimSpace(ns) == "" {
			errs = append(errs, fmt.Errorf("%s.namespaces[%d]: %q is not a namespace name or glob", at, i, ns))
		}
	}
	if rule.NamespaceSelector != nil {
		if _, err := metav1.LabelSelectorAsSelector(rule.NamespaceSelector); err != nil {
			errs = append(errs, fmt.Errorf("%s.namespaceSelector: %w", at, err))
		}
	}
	for i, role := range rule.RequireRoles {
		if role.Name == "" {
			errs = append(errs, fmt.Errorf("%s.requireRoles[%d]: a role needs a name", at, i))
		}
	}
	return errs
}
