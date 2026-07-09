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

func TestPerFixFlagsControlCustomPasses(t *testing.T) {
	origList := EnableListArithmeticUpgrade
	origGeneric := EnableGenericDefaultGuardUpgrade
	origKeep := EnableKeepValidatorsSingletonUpgrade
	origEval := EnableEvalv3SelfRefGuardUpgrade
	t.Cleanup(func() {
		EnableListArithmeticUpgrade = origList
		EnableGenericDefaultGuardUpgrade = origGeneric
		EnableKeepValidatorsSingletonUpgrade = origKeep
		EnableEvalv3SelfRefGuardUpgrade = origEval
	})

	EnableListArithmeticUpgrade = false
	EnableGenericDefaultGuardUpgrade = false
	EnableKeepValidatorsSingletonUpgrade = false
	EnableEvalv3SelfRefGuardUpgrade = false

	got, err := Upgrade(`
_mode: string | *""
if cond { _mode: "x" }
if _mode == "x" { out: true }
x: >=1 & <=1
left: ["a"]
right: ["b"]
both: left + right
`, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if strings.Contains(got, "_modeVal:") || strings.Contains(got, "x: 1") {
		t.Fatalf("expected custom passes disabled, got:\n%s", got)
	}
}
