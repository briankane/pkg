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

// Package nsmatch decides whether a namespace matches a restriction's names and
// selector, as definitions and Packages both restrict by namespace.
package nsmatch

import (
	"path"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// Matches reports whether namespace ns matches names, as names or path.Match
// globs, or selector. Either matching is enough, and neither set matches every
// namespace. Pass nil nsLabels when the namespace could not be read; a selector
// then matches nothing.
func Matches(names []string, selector *metav1.LabelSelector, ns string, nsLabels map[string]string) bool {
	if len(names) == 0 && selector == nil {
		return true
	}
	for _, p := range names {
		if matched, err := path.Match(p, ns); err == nil && matched {
			return true
		}
	}
	if selector == nil || nsLabels == nil {
		return false
	}
	s, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return false
	}
	return s.Matches(labels.Set(nsLabels))
}
