/*
Copyright 2026 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package upgrade

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	origBool := EnableBoolDefaultNegationUpgrade
	EnableBoolDefaultNegationUpgrade = true
	code := m.Run()
	EnableBoolDefaultNegationUpgrade = origBool
	os.Exit(code)
}
