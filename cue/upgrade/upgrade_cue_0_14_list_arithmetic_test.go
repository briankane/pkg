/*
Copyright 2026 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package upgrade

import (
	"strings"
	"testing"
)

func TestListConcatFixedPointWithIndexExpr(t *testing.T) {
	orig := EnableListArithmeticUpgrade
	EnableListArithmeticUpgrade = true
	t.Cleanup(func() { EnableListArithmeticUpgrade = orig })

	input := `
_extraResourcesByAccess: {
	Read: ["arn:aws:kafka:*:*:group/*"]
	Write: []
}
_clusters: ["cluster-a"]
_topics:   ["topic-a"]
access:    "Read"
resources: [
	for c in _clusters {"cluster/\(c)"},
] + _extraResourcesByAccess[access] + [
	for c in _clusters
	for n in _topics {"topic/\(c)/\(n)"},
]
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Concat([") {
		t.Fatalf("expected list.Concat rewrite, got:\n%s", got)
	}
}
