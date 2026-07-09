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
	"cuelang.org/go/cue/token"
)

func upgradeBoolDefaultNegation(cueStr string, file *ast.File) (string, error) {
	rewritten := rewriteBoolDefaultFlags(file)
	result, err := format.Node(rewritten)
	if err != nil {
		return "", fmt.Errorf("failed to format CUE after bool-default-guard-hazard rewrite: %w", err)
	}
	return strings.TrimRight(string(result), "\n"), nil
}

func rewriteBoolDefaultFlags(file *ast.File) *ast.File {
	file.Decls = rewriteDeclList(file.Decls)
	return file
}

func rewriteDeclList(decls []ast.Decl) []ast.Decl {
	flags := detectBoolDefaultFlags(decls)
	if len(flags) == 0 {
		return recurseIntoStructs(decls)
	}
	toRewrite := make(map[string]bool)
	for name := range flags {
		if isUsedInIfCondition(decls, name) {
			toRewrite[name] = true
		}
	}
	if len(toRewrite) == 0 {
		return recurseIntoStructs(decls)
	}
	return applyBoolFlagRewrite(decls, toRewrite, flags)
}

type boolDefaultFlag struct {
	defaultVal bool
}

func detectBoolDefaultFlags(decls []ast.Decl) map[string]boolDefaultFlag {
	result := make(map[string]boolDefaultFlag)
	for _, d := range decls {
		field, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		name := fieldLabelName(field)
		if name == "" {
			continue
		}
		if def, ok := isBoolDefaultExpr(field.Value); ok {
			result[name] = def
		}
	}
	return result
}

func isBoolDefaultExpr(expr ast.Expr) (boolDefaultFlag, bool) {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || bin.Op != token.OR {
		return boolDefaultFlag{}, false
	}
	left, right := bin.X, bin.Y
	if isBoolIdent(left) {
		if def, ok := isDefaultedBoolLit(right); ok {
			return def, true
		}
	}
	if isBoolIdent(right) {
		if def, ok := isDefaultedBoolLit(left); ok {
			return def, true
		}
	}
	return boolDefaultFlag{}, false
}

func isBoolIdent(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "bool"
}

func isDefaultedBoolLit(expr ast.Expr) (boolDefaultFlag, bool) {
	unary, ok := expr.(*ast.UnaryExpr)
	if !ok || unary.Op != token.MUL {
		return boolDefaultFlag{}, false
	}
	lit, ok := unary.X.(*ast.BasicLit)
	if !ok {
		return boolDefaultFlag{}, false
	}
	switch lit.Kind {
	case token.FALSE:
		return boolDefaultFlag{defaultVal: false}, true
	case token.TRUE:
		return boolDefaultFlag{defaultVal: true}, true
	}
	return boolDefaultFlag{}, false
}

func isUsedInIfCondition(decls []ast.Decl, name string) bool {
	found := false
	astutil.Apply(&ast.File{Decls: decls}, func(c astutil.Cursor) bool {
		comp, ok := c.Node().(*ast.Comprehension)
		if !ok {
			return true
		}
		for _, clause := range comp.Clauses {
			ifClause, ok := clause.(*ast.IfClause)
			if !ok {
				continue
			}
			if conditionReferencesBool(ifClause.Condition, name) {
				found = true
				return false
			}
		}
		return true
	}, nil)
	return found
}

func conditionReferencesBool(expr ast.Expr, name string) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == name
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			if id, ok := e.X.(*ast.Ident); ok {
				return id.Name == name
			}
		}
	}
	return false
}

func applyBoolFlagRewrite(decls []ast.Decl, toRewrite map[string]bool, flags map[string]boolDefaultFlag) []ast.Decl {
	type strategy int
	const (
		strategyDirect   strategy = iota
		strategySentinel strategy = iota
	)
	type flagPlan struct {
		mode     strategy
		condExpr ast.Expr
		defaultV bool
	}
	plans := make(map[string]flagPlan, len(toRewrite))
	for name := range toRewrite {
		condExpr, ok := extractSingleConditionChain(decls, name)
		if ok {
			plans[name] = flagPlan{mode: strategyDirect, condExpr: condExpr, defaultV: flags[name].defaultVal}
		} else {
			plans[name] = flagPlan{mode: strategySentinel, defaultV: flags[name].defaultVal}
		}
	}

	var out []ast.Decl
	for _, d := range decls {
		field, ok := d.(*ast.Field)
		if ok {
			name := fieldLabelName(field)
			if plan, matched := plans[name]; matched {
				if _, isBool := isBoolDefaultExpr(field.Value); isBool {
					switch plan.mode {
					case strategyDirect:
						out = append(out, makeDirectExprDecl(name, plan.condExpr, plan.defaultV))
					case strategySentinel:
						helperName := helperVarName(name)
						out = append(out, makeHelperDecl(helperName, plan.defaultV))
						out = append(out, makeComparisonDecl(name, helperName))
					}
					continue
				}
			}
		}

		comp, isComp := d.(*ast.Comprehension)
		if isComp {
			dropped := false
			for name, plan := range plans {
				if plan.mode == strategyDirect && comprehensionSetsFlag(comp, name) {
					dropped = true
					break
				}
			}
			if dropped {
				continue
			}
		}

		sentinelNames := make(map[string]bool)
		for name, plan := range plans {
			if plan.mode == strategySentinel {
				sentinelNames[name] = true
			}
		}
		if len(sentinelNames) > 0 {
			d = rewriteAssignmentsInDecl(d, sentinelNames)
		}

		if field, ok := d.(*ast.Field); ok {
			if structLit, ok := field.Value.(*ast.StructLit); ok {
				structLit.Elts = rewriteDeclList(structLit.Elts)
			}
		}

		out = append(out, d)
	}
	return out
}

func extractSingleConditionChain(decls []ast.Decl, flagName string) (ast.Expr, bool) {
	var settingComps []*ast.Comprehension
	for _, d := range decls {
		comp, ok := d.(*ast.Comprehension)
		if !ok {
			if field, ok := d.(*ast.Field); ok {
				if fieldLabelName(field) == flagName {
					if _, isBool := isBoolDefaultExpr(field.Value); !isBool {
						return nil, false
					}
				}
			}
			continue
		}
		if comprehensionSetsFlag(comp, flagName) {
			settingComps = append(settingComps, comp)
		}
	}
	if len(settingComps) != 1 {
		return nil, false
	}
	return extractConditionChainFromComp(settingComps[0], flagName)
}

func comprehensionSetsFlag(comp *ast.Comprehension, flagName string) bool {
	found := false
	astutil.Apply(comp, func(c astutil.Cursor) bool {
		field, ok := c.Node().(*ast.Field)
		if !ok {
			return true
		}
		if fieldLabelName(field) != flagName {
			return true
		}
		lit, ok := field.Value.(*ast.BasicLit)
		if ok && (lit.Kind == token.TRUE || lit.Kind == token.FALSE) {
			found = true
			return false
		}
		return true
	}, nil)
	return found
}

func extractConditionChainFromComp(comp *ast.Comprehension, flagName string) (ast.Expr, bool) {
	var conditions []ast.Expr
	for _, clause := range comp.Clauses {
		ifClause, ok := clause.(*ast.IfClause)
		if !ok {
			return nil, false
		}
		conditions = append(conditions, ifClause.Condition)
	}

	body, ok := comp.Value.(*ast.StructLit)
	if !ok {
		return nil, false
	}
	if len(body.Elts) != 1 {
		return nil, false
	}

	var innerCond ast.Expr
	switch elt := body.Elts[0].(type) {
	case *ast.Field:
		if fieldLabelName(elt) != flagName {
			return nil, false
		}
		lit, ok := elt.Value.(*ast.BasicLit)
		if !ok || lit.Kind != token.TRUE {
			return nil, false
		}
		innerCond = nil
	case *ast.Comprehension:
		inner, ok := extractConditionChainFromComp(elt, flagName)
		if !ok {
			return nil, false
		}
		innerCond = inner
	default:
		return nil, false
	}

	result := andConditions(conditions)
	if innerCond != nil {
		result = &ast.BinaryExpr{X: result, Op: token.LAND, Y: innerCond}
	}
	return result, true
}

func andConditions(conds []ast.Expr) ast.Expr {
	if len(conds) == 0 {
		return nil
	}
	result := conds[0]
	for _, c := range conds[1:] {
		result = &ast.BinaryExpr{X: result, Op: token.LAND, Y: c}
	}
	return result
}

func makeDirectExprDecl(flagName string, condExpr ast.Expr, _ bool) *ast.Field {
	return &ast.Field{
		Label: ast.NewIdent(flagName),
		Value: condExpr,
	}
}

func rewriteAssignmentsInDecl(decl ast.Decl, toRewrite map[string]bool) ast.Decl {
	astutil.Apply(decl, func(c astutil.Cursor) bool {
		field, ok := c.Node().(*ast.Field)
		if !ok {
			return true
		}
		name := fieldLabelName(field)
		if !toRewrite[name] {
			return true
		}
		lit, ok := field.Value.(*ast.BasicLit)
		if !ok {
			return true
		}
		var sentinel string
		switch lit.Kind {
		case token.TRUE:
			sentinel = "true"
		case token.FALSE:
			sentinel = ""
		default:
			return true
		}
		helperName := helperVarName(name)
		field.Label = ast.NewIdent(helperName)
		field.Value = &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", sentinel)}
		return true
	}, nil)
	return decl
}

func recurseIntoStructs(decls []ast.Decl) []ast.Decl {
	for _, d := range decls {
		field, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		if structLit, ok := field.Value.(*ast.StructLit); ok {
			structLit.Elts = rewriteDeclList(structLit.Elts)
		}
	}
	return decls
}

func helperVarName(flagName string) string { return flagName + "Val" }

func makeHelperDecl(helperName string, defaultVal bool) *ast.Field {
	sentinel := `""`
	if defaultVal {
		sentinel = `"true"`
	}
	return &ast.Field{
		Label: ast.NewIdent(helperName),
		Value: &ast.BinaryExpr{
			X:  &ast.UnaryExpr{Op: token.MUL, X: &ast.BasicLit{Kind: token.STRING, Value: sentinel}},
			Op: token.OR,
			Y:  ast.NewIdent("string"),
		},
	}
}

func makeComparisonDecl(flagName, helperName string) *ast.Field {
	return &ast.Field{
		Label: ast.NewIdent(flagName),
		Value: &ast.BinaryExpr{
			X:  ast.NewIdent(helperName),
			Op: token.EQL,
			Y:  &ast.BasicLit{Kind: token.STRING, Value: `"true"`},
		},
	}
}

func fieldLabelName(field *ast.Field) string {
	if l, ok := field.Label.(*ast.Ident); ok {
		return l.Name
	}
	return ""
}
