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

func TestGenericDefaultGuardHazard(t *testing.T) {
	orig := EnableGenericDefaultGuardUpgrade
	EnableGenericDefaultGuardUpgrade = true
	t.Cleanup(func() { EnableGenericDefaultGuardUpgrade = orig })

	input := `
_mode: string | *""
if parameter.cluster != _|_ {
	_mode: "secondary"
}
if _mode == "secondary" {
	output: "secondary path"
}
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "_modeVal: string | *\"\"") {
		t.Fatalf("expected sentinel rewrite, got:\n%s", got)
	}
}
