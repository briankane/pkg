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

func upgradeGenericDefaultGuardCompat(cueStr string, _ *ast.File) (string, error) {
	return rewriteGenericDefaultGuardHazards(cueStr)
}

func rewriteGenericDefaultGuardHazards(cueStr string) (string, error) {
	file, err := parser.ParseFile("", cueStr, parser.ParseComments)
	if err != nil {
		return "", err
	}
	newDecls, changed := rewriteGenericDefaultsInDecls(file.Decls)
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

func rewriteGenericDefaultsInDecls(decls []ast.Decl) ([]ast.Decl, bool) {
	type defaultField struct{ defaultExpr ast.Expr }
	candidates := map[string]defaultField{}
	for _, d := range decls {
		f, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		name := fieldIdentName(f.Label)
		if name == "" || name == "bool" {
			continue
		}
		typeExpr, isDefaulted := extractDefaultedTypeExpr(f.Value)
		if !isDefaulted || isBoolTypeExpr(typeExpr) {
			continue
		}
		candidates[name] = defaultField{defaultExpr: f.Value}
	}
	usedInGuard, hasConditionalAssignment := map[string]bool{}, map[string]bool{}
	for _, d := range decls {
		comp, ok := d.(*ast.Comprehension)
		if !ok {
			continue
		}
		for name := range candidates {
			if comprehensionConditionReferences(comp, name) {
				usedInGuard[name] = true
			}
			if comprehensionAssignsField(comp, name) {
				hasConditionalAssignment[name] = true
			}
		}
	}
	hazardHelpers := map[string]string{}
	for name := range candidates {
		if usedInGuard[name] && hasConditionalAssignment[name] {
			hazardHelpers[name] = name + "Val"
		}
	}
	changed := false
	out := make([]ast.Decl, 0, len(decls)+len(hazardHelpers))
	for _, d := range decls {
		if f, ok := d.(*ast.Field); ok {
			name := fieldIdentName(f.Label)
			if helper, hazard := hazardHelpers[name]; hazard {
				if c, exists := candidates[name]; exists {
					out = append(out, &ast.Field{Label: ast.NewIdent(helper), Value: c.defaultExpr}, &ast.Field{Label: ast.NewIdent(name), Value: ast.NewIdent(helper)})
					changed = true
					continue
				}
			}
		}
		d = rewriteGenericAssignmentsInDecl(d, hazardHelpers)
		if f, ok := d.(*ast.Field); ok {
			if s, ok := unwrapParenExpr(f.Value).(*ast.StructLit); ok {
				var ch bool
				s.Elts, ch = rewriteGenericDefaultsInDecls(s.Elts)
				changed = changed || ch
			}
		}
		out = append(out, d)
	}
	return out, changed
}

func rewriteGenericAssignmentsInDecl(decl ast.Decl, hazardHelpers map[string]string) ast.Decl {
	astutil.Apply(decl, func(c astutil.Cursor) bool {
		f, ok := c.Node().(*ast.Field)
		if !ok {
			return true
		}
		name := fieldIdentName(f.Label)
		helper, hazard := hazardHelpers[name]
		if hazard {
			f.Label = ast.NewIdent(helper)
		}
		return true
	}, nil)
	return decl
}

func extractDefaultedTypeExpr(expr ast.Expr) (ast.Expr, bool) {
	bin, ok := unwrapParenExpr(expr).(*ast.BinaryExpr)
	if !ok || bin.Op != token.OR {
		return nil, false
	}
	if isDefaultExpr(bin.X) {
		return bin.Y, true
	}
	if isDefaultExpr(bin.Y) {
		return bin.X, true
	}
	return nil, false
}

func isBoolTypeExpr(expr ast.Expr) bool {
	id, ok := unwrapParenExpr(expr).(*ast.Ident)
	return ok && id.Name == "bool"
}

func comprehensionConditionReferences(comp *ast.Comprehension, name string) bool {
	for _, clause := range comp.Clauses {
		ifClause, ok := clause.(*ast.IfClause)
		if ok && exprReferencesIdent(ifClause.Condition, name) {
			return true
		}
	}
	return false
}

func comprehensionAssignsField(comp *ast.Comprehension, name string) bool {
	found := false
	astutil.Apply(comp, func(c astutil.Cursor) bool {
		f, ok := c.Node().(*ast.Field)
		if ok && fieldIdentName(f.Label) == name {
			found = true
			return false
		}
		return true
	}, nil)
	return found
}
