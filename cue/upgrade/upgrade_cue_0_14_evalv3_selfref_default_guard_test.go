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

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func TestEvalv3SelfRefGuard(t *testing.T) {
	orig := EnableEvalv3SelfRefGuardUpgrade
	EnableEvalv3SelfRefGuardUpgrade = true
	t.Cleanup(func() { EnableEvalv3SelfRefGuardUpgrade = orig })

	base := `
x: *45 | int & {
	if x < 1 { _|_ & {errorMessage: "x must be >= 1"} }
}
`
	upgraded, err := Upgrade(base, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if strings.Contains(upgraded, "x: *45 | int & {") {
		t.Fatalf("expected evalv3 rewrite, got:\n%s", upgraded)
	}
	bad := cuecontext.New().CompileString(upgraded + "\nx: 0\n")
	if bad.Err() == nil && bad.Validate(cue.Concrete(false)) == nil {
		t.Fatalf("expected x=0 to fail after rewrite:\n%s", upgraded)
	}
}
