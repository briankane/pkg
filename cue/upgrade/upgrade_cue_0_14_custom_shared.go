/*
Copyright 2026 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package upgrade

import (
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/token"
)

func unwrapParenExpr(expr ast.Expr) ast.Expr {
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = p.X
	}
}

func isDefaultExpr(expr ast.Expr) bool {
	u, ok := unwrapParenExpr(expr).(*ast.UnaryExpr)
	return ok && u.Op == token.MUL
}

func fieldIdentName(label ast.Label) string {
	id, ok := label.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

func exprReferencesIdent(expr ast.Expr, name string) bool {
	found := false
	astutil.Apply(expr, func(c astutil.Cursor) bool {
		id, ok := c.Node().(*ast.Ident)
		if ok && id.Name == name {
			found = true
			return false
		}
		return true
	}, nil)
	return found
}
