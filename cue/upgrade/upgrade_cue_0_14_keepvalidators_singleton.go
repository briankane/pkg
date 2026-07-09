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

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
)

func upgradeKeepValidatorsSingletonCompat(cueStr string, _ *ast.File) (string, error) {
	return rewriteKeepValidatorsSingletons(cueStr)
}

func rewriteKeepValidatorsSingletons(cueStr string) (string, error) {
	file, err := parser.ParseFile("", cueStr, parser.ParseComments)
	if err != nil {
		return "", err
	}
	newDecls, changed := rewriteKeepValidatorsInDecls(file.Decls)
	file.Decls = newDecls
	if !changed {
		return cueStr, nil
	}
	out, err := format.Node(file)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func rewriteKeepValidatorsInDecls(decls []ast.Decl) ([]ast.Decl, bool) {
	changed := false
	out := make([]ast.Decl, 0, len(decls))
	for _, d := range decls {
		if f, ok := d.(*ast.Field); ok {
			if singleton := extractSingletonConstraintValue(f.Value); singleton != nil {
				f.Value, changed = singleton, true
			}
			if s, ok := unwrapParenExpr(f.Value).(*ast.StructLit); ok {
				newElts, ch := rewriteKeepValidatorsInDecls(s.Elts)
				s.Elts = newElts
				changed = changed || ch
			}
		}
		out = append(out, d)
	}
	return out, changed
}

func extractSingletonConstraintValue(expr ast.Expr) ast.Expr {
	bin, ok := unwrapParenExpr(expr).(*ast.BinaryExpr)
	if !ok || bin.Op != token.AND {
		return nil
	}
	low, high, ok := parseConstraintBounds(bin.X, bin.Y)
	if !ok || low == nil || high == nil || low.Kind != high.Kind || low.Value != high.Value {
		return nil
	}
	return &ast.BasicLit{Kind: low.Kind, Value: low.Value}
}

func parseConstraintBounds(a, b ast.Expr) (low, high *ast.BasicLit, ok bool) {
	la, ha := parseConstraintBound(a)
	lb, hb := parseConstraintBound(b)
	if la != nil {
		low = la
	}
	if lb != nil {
		low = lb
	}
	if ha != nil {
		high = ha
	}
	if hb != nil {
		high = hb
	}
	return low, high, low != nil && high != nil
}

func parseConstraintBound(expr ast.Expr) (low, high *ast.BasicLit) {
	u, ok := unwrapParenExpr(expr).(*ast.UnaryExpr)
	if !ok {
		return nil, nil
	}
	lit, ok := u.X.(*ast.BasicLit)
	if !ok {
		return nil, nil
	}
	switch u.Op {
	case token.GEQ:
		return lit, nil
	case token.LEQ:
		return nil, lit
	default:
		return nil, nil
	}
}
