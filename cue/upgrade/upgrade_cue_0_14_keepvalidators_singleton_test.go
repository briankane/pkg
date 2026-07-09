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

func TestKeepValidatorsSingleton(t *testing.T) {
	orig := EnableKeepValidatorsSingletonUpgrade
	EnableKeepValidatorsSingletonUpgrade = true
	t.Cleanup(func() { EnableKeepValidatorsSingletonUpgrade = orig })

	got, err := Upgrade("x: >=1 & <=1\ny: x + 1\n", Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "x: 1") {
		t.Fatalf("expected concretized singleton, got:\n%s", got)
	}
}
