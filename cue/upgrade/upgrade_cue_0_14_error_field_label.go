/*
Copyright 2026 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package upgrade

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/format"
)

func upgradeErrorFieldLabel(cueStr string, file *ast.File) (string, error) {
	astutil.Apply(file, func(cursor astutil.Cursor) bool {
		field, ok := cursor.Node().(*ast.Field)
		if !ok {
			return true
		}
		ident, ok := field.Label.(*ast.Ident)
		if !ok || ident.Name != "error" {
			return true
		}
		field.Label = ast.NewString("error")
		return true
	}, nil)

	result, err := format.Node(file)
	if err != nil {
		return "", fmt.Errorf("failed to format CUE: %w", err)
	}
	return strings.TrimRight(string(result), "\n"), nil
}
