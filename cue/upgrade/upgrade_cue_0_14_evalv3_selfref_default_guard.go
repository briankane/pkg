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
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
)

func upgradeEvalv3SelfRefGuardCompat(cueStr string, _ *ast.File) (string, error) {
	return rewriteEvalv3SelfRefDefaultGuards(cueStr)
}

func rewriteEvalv3SelfRefDefaultGuards(cueStr string) (string, error) {
	file, err := parser.ParseFile("", cueStr, parser.ParseComments)
	if err != nil {
		return "", err
	}
	newDecls, changed := rewriteEvalv3Decls(file.Decls)
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

func rewriteEvalv3Decls(decls []ast.Decl) ([]ast.Decl, bool) {
	changed := false
	out := make([]ast.Decl, 0, len(decls)+2)
	for _, d := range decls {
		f, ok := d.(*ast.Field)
		if !ok {
			out = append(out, d)
			continue
		}
		if s, ok := unwrapParenExpr(f.Value).(*ast.StructLit); ok {
			newElts, ch := rewriteEvalv3Decls(s.Elts)
			s.Elts = newElts
			changed = changed || ch
		}
		name := fieldIdentName(f.Label)
		helperExpr, guardStruct, ok := extractEvalv3GuardParts(f.Value, name)
		if !ok {
			out = append(out, d)
			continue
		}
		f.Value = helperExpr
		for _, elt := range guardStruct.Elts {
			if comp, ok := elt.(*ast.Comprehension); ok {
				out = append(out, comp)
			}
		}
		out, changed = append(out, f), true
	}
	return out, changed
}

func extractEvalv3GuardParts(expr ast.Expr, fieldName string) (ast.Expr, *ast.StructLit, bool) {
	orExpr, ok := unwrapParenExpr(expr).(*ast.BinaryExpr)
	if !ok || orExpr.Op != token.OR {
		return nil, nil, false
	}
	leftNonDefault, rightNonDefault := !isDefaultExpr(orExpr.X), !isDefaultExpr(orExpr.Y)
	if leftNonDefault == rightNonDefault {
		return nil, nil, false
	}
	var defaultExpr, nonDefaultExpr ast.Expr
	if isDefaultExpr(orExpr.X) {
		defaultExpr, nonDefaultExpr = orExpr.X, orExpr.Y
	} else {
		defaultExpr, nonDefaultExpr = orExpr.Y, orExpr.X
	}
	typeExpr, guardStruct, ok := splitTypeAndGuardStruct(nonDefaultExpr)
	if !ok || !structContainsSelfRefGuard(guardStruct, fieldName) {
		return nil, nil, false
	}
	return &ast.BinaryExpr{X: defaultExpr, Op: token.OR, Y: typeExpr}, guardStruct, true
}

func splitTypeAndGuardStruct(expr ast.Expr) (ast.Expr, *ast.StructLit, bool) {
	andExpr, ok := unwrapParenExpr(expr).(*ast.BinaryExpr)
	if !ok || andExpr.Op != token.AND {
		return nil, nil, false
	}
	if s, ok := unwrapParenExpr(andExpr.X).(*ast.StructLit); ok {
		return andExpr.Y, s, true
	}
	if s, ok := unwrapParenExpr(andExpr.Y).(*ast.StructLit); ok {
		return andExpr.X, s, true
	}
	return nil, nil, false
}

func structContainsSelfRefGuard(s *ast.StructLit, fieldName string) bool {
	found := false
	astutil.Apply(s, func(c astutil.Cursor) bool {
		comp, ok := c.Node().(*ast.Comprehension)
		if !ok {
			return true
		}
		for _, clause := range comp.Clauses {
			ifClause, ok := clause.(*ast.IfClause)
			if ok && exprReferencesIdent(ifClause.Condition, fieldName) {
				found = true
				return false
			}
		}
		return true
	}, nil)
	return found
}
