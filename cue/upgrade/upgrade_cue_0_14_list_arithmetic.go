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
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
)

func upgradeListConcatenation(cueStr string, file *ast.File) (string, error) {
	transformed := upgradeListConcatenationAST(file)
	result, err := format.Node(transformed)
	if err != nil {
		return "", fmt.Errorf("failed to format CUE: %w", err)
	}
	return rewriteListConcatChainsFixedPoint(strings.TrimRight(string(result), "\n"))
}

func upgradeListConcatenationAST(file *ast.File) *ast.File {
	listRegistry := collectListDeclarations(file)
	needsListImport := false

	result := astutil.Apply(file, func(cursor astutil.Cursor) bool {
		binExpr, ok := cursor.Node().(*ast.BinaryExpr)
		if !ok {
			return true
		}

		if binExpr.Op.String() == "+" {
			operands := collectAddChain(binExpr, listRegistry)
			if len(operands) >= 2 {
				cursor.Replace(&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.Ident{Name: "list"},
						Sel: &ast.Ident{Name: "Concat"},
					},
					Args: []ast.Expr{&ast.ListLit{Elts: operands}},
				})
				needsListImport = true
			}
		}

		if binExpr.Op.String() == "*" {
			var listExpr, countExpr ast.Expr
			if isStrongListExpression(binExpr.X, listRegistry) && isNumericExpression(binExpr.Y, listRegistry) {
				listExpr, countExpr = binExpr.X, binExpr.Y
			} else if isNumericExpression(binExpr.X, listRegistry) && isStrongListExpression(binExpr.Y, listRegistry) {
				countExpr, listExpr = binExpr.X, binExpr.Y
			}
			if listExpr != nil {
				ast.SetRelPos(listExpr, 0)
				ast.SetRelPos(countExpr, 0)
				cursor.Replace(&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.Ident{Name: "list"},
						Sel: &ast.Ident{Name: "Repeat"},
					},
					Args: []ast.Expr{listExpr, countExpr},
				})
				needsListImport = true
			}
		}

		return true
	}, nil)

	if f, ok := result.(*ast.File); ok && needsListImport {
		ensureListImport(f)
		return f
	}
	return file
}

func collectAddChain(expr ast.Expr, listRegistry map[string]bool) []ast.Expr {
	operands, hasStrong := collectAddChainInner(expr, listRegistry)
	if operands == nil || !hasStrong {
		return nil
	}
	return operands
}

func collectAddChainInner(expr ast.Expr, listRegistry map[string]bool) ([]ast.Expr, bool) {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || bin.Op.String() != "+" {
		if isListExpression(expr, listRegistry) {
			return extractListConcatArgs(expr), isStrongListExpression(expr, listRegistry)
		}
		return nil, false
	}
	left, leftStrong := collectAddChainInner(bin.X, listRegistry)
	if left == nil {
		return nil, false
	}
	if !isListExpression(bin.Y, listRegistry) {
		return nil, false
	}
	return append(left, extractListConcatArgs(bin.Y)...), leftStrong || isStrongListExpression(bin.Y, listRegistry)
}

func isStrongListExpression(expr ast.Expr, listRegistry map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.ListLit:
		return true
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "list" {
				return true
			}
		}
		return false
	case *ast.Ident:
		return listRegistry[e.Name]
	case *ast.SelectorExpr:
		root := selectorRootIdent(e)
		if root == "context" || root == "parameter" {
			return true
		}
		return isListExpression(expr, listRegistry)
	default:
		return false
	}
}

func extractListConcatArgs(expr ast.Expr) []ast.Expr {
	callExpr, ok := expr.(*ast.CallExpr)
	if !ok {
		return []ast.Expr{expr}
	}
	sel, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return []ast.Expr{expr}
	}
	funIdent, ok := sel.X.(*ast.Ident)
	if !ok || funIdent.Name != "list" {
		return []ast.Expr{expr}
	}
	methodIdent, ok := sel.Sel.(*ast.Ident)
	if !ok || methodIdent.Name != "Concat" {
		return []ast.Expr{expr}
	}
	if len(callExpr.Args) != 1 {
		return []ast.Expr{expr}
	}
	listLit, ok := callExpr.Args[0].(*ast.ListLit)
	if !ok {
		return []ast.Expr{expr}
	}
	return listLit.Elts
}

func ensureListImport(file *ast.File) {
	for _, decl := range file.Decls {
		importDecl, ok := decl.(*ast.ImportDecl)
		if !ok {
			continue
		}
		for _, spec := range importDecl.Specs {
			if spec.Path.Value == `"list"` {
				return
			}
		}
	}
	file.Decls = append([]ast.Decl{
		&ast.ImportDecl{
			Specs: []*ast.ImportSpec{
				{Path: ast.NewString("list")},
			},
		},
	}, file.Decls...)
}

func collectListDeclarations(file *ast.File) map[string]bool {
	listRegistry := make(map[string]bool)
	nonListTopLevel := make(map[string]bool)
	for _, decl := range file.Decls {
		field, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		ident, ok := field.Label.(*ast.Ident)
		if !ok {
			continue
		}
		switch value := field.Value.(type) {
		case *ast.ListLit:
			listRegistry[ident.Name] = true
		case *ast.StructLit:
			collectNestedListDeclarationsFirstPass(value, ident.Name, listRegistry)
			collectNestedListDeclarationsSecondPass(value, ident.Name, listRegistry)
		case *ast.BinaryExpr, *ast.CallExpr:
			if isListOperationResult(value, listRegistry) {
				listRegistry[ident.Name] = true
			} else {
				nonListTopLevel[ident.Name] = true
			}
		default:
			nonListTopLevel[ident.Name] = true
		}
	}
	for name := range nonListTopLevel {
		delete(listRegistry, name)
	}
	return listRegistry
}

func collectNestedListDeclarationsFirstPass(structLit *ast.StructLit, prefix string, listRegistry map[string]bool) {
	for _, elt := range structLit.Elts {
		field, ok := elt.(*ast.Field)
		if !ok {
			continue
		}
		key, ok := field.Label.(*ast.Ident)
		if !ok {
			continue
		}
		fullPath := prefix + "." + key.Name
		switch value := field.Value.(type) {
		case *ast.ListLit:
			addListPathAliases(listRegistry, fullPath)
		case *ast.StructLit:
			collectNestedListDeclarationsFirstPass(value, fullPath, listRegistry)
		default:
			if isListLiteral(value) {
				addListPathAliases(listRegistry, fullPath)
			}
		}
	}
}

func collectNestedListDeclarationsSecondPass(structLit *ast.StructLit, prefix string, listRegistry map[string]bool) bool {
	changed := false
	for _, elt := range structLit.Elts {
		field, ok := elt.(*ast.Field)
		if !ok {
			continue
		}
		key, ok := field.Label.(*ast.Ident)
		if !ok {
			continue
		}
		fullPath := prefix + "." + key.Name
		switch value := field.Value.(type) {
		case *ast.BinaryExpr, *ast.CallExpr:
			if isListOperationResult(value, listRegistry) && !listRegistry[fullPath] {
				addListPathAliases(listRegistry, fullPath)
				changed = true
			}
		case *ast.StructLit:
			if collectNestedListDeclarationsSecondPass(value, fullPath, listRegistry) {
				changed = true
			}
		}
	}
	return changed
}

func isListLiteral(expr ast.Expr) bool {
	expr = unwrapParenExpr(expr)
	switch e := expr.(type) {
	case *ast.ListLit, *ast.Comprehension:
		return true
	case *ast.UnaryExpr:
		return e.Op == token.MUL && isListLiteral(e.X)
	case *ast.BinaryExpr:
		return e.Op == token.OR && (isListLiteral(e.X) || isListLiteral(e.Y))
	}
	return false
}

func isListExpression(expr ast.Expr, listRegistry map[string]bool) bool {
	if isListLiteral(expr) {
		return true
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return listRegistry[ident.Name]
	}
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		root := selectorRootIdent(selector)
		if root == "context" || root == "parameter" {
			if root == "context" {
				return true
			}
			if key := selectorPath(selector); key != "" && listRegistry[key] {
				return true
			}
			return false
		}
		if key := selectorPath(selector); key != "" && listRegistry[key] {
			return true
		}
	}
	if callExpr, ok := expr.(*ast.CallExpr); ok {
		if sel, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "list" {
				if method, ok := sel.Sel.(*ast.Ident); ok && (method.Name == "Concat" || method.Name == "Repeat") {
					return true
				}
			}
		}
	}
	return false
}

func selectorRootIdent(e *ast.SelectorExpr) string {
	cur := e.X
	for {
		if id, ok := cur.(*ast.Ident); ok {
			return id.Name
		}
		sel, ok := cur.(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		cur = sel.X
	}
}

func isNumericExpression(expr ast.Expr, listRegistry map[string]bool) bool {
	expr = unwrapParenExpr(expr)
	if _, ok := expr.(*ast.BasicLit); ok {
		return true
	}
	if unary, ok := expr.(*ast.UnaryExpr); ok {
		return unary.Op == token.SUB || unary.Op == token.ADD
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		return !isListExpression(sel, listRegistry)
	}
	if bin, ok := expr.(*ast.BinaryExpr); ok && bin.Op == token.OR {
		leftNumeric := isNumericExpression(bin.X, listRegistry)
		rightNumeric := isNumericExpression(bin.Y, listRegistry)
		if leftNumeric || rightNumeric {
			return true
		}
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return !listRegistry[ident.Name]
	}
	if _, ok := expr.(*ast.BinaryExpr); ok {
		return true
	}
	if callExpr, ok := expr.(*ast.CallExpr); ok {
		if sel, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "math" {
				return true
			}
		}
	}
	return false
}

func isListOperationResult(expr ast.Expr, listRegistry map[string]bool) bool {
	if binExpr, ok := expr.(*ast.BinaryExpr); ok {
		if binExpr.Op.String() == "+" {
			return isListExpression(binExpr.X, listRegistry) && isListExpression(binExpr.Y, listRegistry) &&
				(isStrongListExpression(binExpr.X, listRegistry) || isStrongListExpression(binExpr.Y, listRegistry))
		}
		if binExpr.Op.String() == "*" {
			return (isStrongListExpression(binExpr.X, listRegistry) && isNumericExpression(binExpr.Y, listRegistry)) ||
				(isNumericExpression(binExpr.X, listRegistry) && isStrongListExpression(binExpr.Y, listRegistry))
		}
	}
	if callExpr, ok := expr.(*ast.CallExpr); ok {
		if sel, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "list" {
				if selName, ok := sel.Sel.(*ast.Ident); ok {
					return selName.Name == "Concat" || selName.Name == "Repeat"
				}
			}
		}
	}
	return false
}

func addListPathAliases(reg map[string]bool, fullPath string) {
	parts := strings.Split(fullPath, ".")
	for i := range parts {
		alias := strings.Join(parts[i:], ".")
		reg[alias] = true
	}
}

func rewriteListConcatChainsFixedPoint(cueStr string) (string, error) {
	file, err := parser.ParseFile("", cueStr, parser.ParseComments)
	if err != nil {
		return "", err
	}
	changed := false
	for range 10 {
		listNames, mapOfListsNames := collectLocalListFacts(file)
		passChanged := false
		astutil.Apply(file, func(cursor astutil.Cursor) bool {
			bin, ok := cursor.Node().(*ast.BinaryExpr)
			if !ok || bin.Op != token.ADD {
				return true
			}
			operands := flattenAddChain(bin)
			if len(operands) < 2 {
				return true
			}
			hasStrong := false
			for _, op := range operands {
				if !isListLikeExpr(op, listNames, mapOfListsNames) {
					return true
				}
				if isStrongListLikeExpr(op, listNames, mapOfListsNames) {
					hasStrong = true
				}
			}
			if !hasStrong {
				return true
			}
			cursor.Replace(buildConcatCall(operands))
			passChanged = true
			return true
		}, nil)
		if !passChanged {
			break
		}
		changed = true
	}
	if !changed {
		return cueStr, nil
	}
	ensureListImport(file)
	out, err := format.Node(file)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func collectLocalListFacts(file *ast.File) (map[string]bool, map[string]bool) {
	listNames := map[string]bool{}
	mapOfListsNames := map[string]bool{}
	nonListNames := map[string]bool{}
	collectFactsFromDecls(file.Decls, "", listNames, mapOfListsNames, nonListNames)
	for name := range nonListNames {
		if !listNames[name] {
			delete(mapOfListsNames, name)
		}
	}
	return listNames, mapOfListsNames
}

func collectFactsFromDecls(decls []ast.Decl, prefix string, listNames, mapOfListsNames, nonListNames map[string]bool) {
	for _, d := range decls {
		f, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		id, ok := f.Label.(*ast.Ident)
		if !ok {
			continue
		}
		name := id.Name
		qualified := name
		if prefix != "" {
			qualified = prefix + "." + name
		}
		switch v := unwrapParenExpr(f.Value).(type) {
		case *ast.StructLit:
			if structLooksLikeMapOfLists(v) {
				mapOfListsNames[name], mapOfListsNames[qualified] = true, true
			}
			collectFactsFromDecls(v.Elts, qualified, listNames, mapOfListsNames, nonListNames)
		default:
			if isMapOfListsDefExpr(v, mapOfListsNames) {
				mapOfListsNames[name], mapOfListsNames[qualified] = true, true
				continue
			}
			if isListLiteralLike(v) || isListConcatCall(v) {
				listNames[name] = true
			} else {
				nonListNames[name] = true
			}
		}
	}
}

func structLooksLikeMapOfLists(s *ast.StructLit) bool {
	if len(s.Elts) == 0 {
		return false
	}
	for _, e := range s.Elts {
		f, ok := e.(*ast.Field)
		if !ok {
			return false
		}
		v := unwrapParenExpr(f.Value)
		if isListLiteralLike(v) || isListConcatCall(v) {
			continue
		}
		if nested, ok := v.(*ast.StructLit); ok && structLooksLikeMapOfLists(nested) {
			continue
		}
		if isMapOfListsDefExpr(v, map[string]bool{}) {
			continue
		}
		return false
	}
	return true
}

func isListLiteralLike(expr ast.Expr) bool {
	switch unwrapParenExpr(expr).(type) {
	case *ast.ListLit, *ast.Comprehension:
		return true
	default:
		return false
	}
}

func isListConcatCall(expr ast.Expr) bool {
	c, ok := unwrapParenExpr(expr).(*ast.CallExpr)
	if !ok || len(c.Args) != 1 {
		return false
	}
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	base, ok := sel.X.(*ast.Ident)
	if !ok || base.Name != "list" {
		return false
	}
	method, ok := sel.Sel.(*ast.Ident)
	return ok && method.Name == "Concat"
}

func flattenAddChain(expr ast.Expr) []ast.Expr {
	expr = unwrapParenExpr(expr)
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || bin.Op != token.ADD {
		return flattenConcatOperand(expr)
	}
	return append(flattenAddChain(bin.X), flattenAddChain(bin.Y)...)
}

func flattenConcatOperand(expr ast.Expr) []ast.Expr {
	expr = unwrapParenExpr(expr)
	call, ok := expr.(*ast.CallExpr)
	if !ok || !isListConcatCall(call) {
		return []ast.Expr{expr}
	}
	listArg, ok := call.Args[0].(*ast.ListLit)
	if !ok {
		return []ast.Expr{expr}
	}
	return listArg.Elts
}

func isListLikeExpr(expr ast.Expr, listNames, mapOfListsNames map[string]bool) bool {
	expr = unwrapParenExpr(expr)
	if isListLiteralLike(expr) || isListConcatCall(expr) {
		return true
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return listNames[e.Name]
	case *ast.IndexExpr:
		return isMapOfListsExpr(e.X, mapOfListsNames)
	}
	return false
}

func isStrongListLikeExpr(expr ast.Expr, listNames, mapOfListsNames map[string]bool) bool {
	return isListLikeExpr(expr, listNames, mapOfListsNames)
}

func isMapOfListsDefExpr(expr ast.Expr, mapOfListsNames map[string]bool) bool {
	expr = unwrapParenExpr(expr)
	switch e := expr.(type) {
	case *ast.Ident:
		return mapOfListsNames[e.Name]
	case *ast.SelectorExpr:
		key := selectorPath(e)
		return key != "" && mapOfListsNames[key]
	case *ast.BinaryExpr:
		return e.Op == token.AND && isMapOfListsDefExpr(e.X, mapOfListsNames) && isMapOfListsDefExpr(e.Y, mapOfListsNames)
	case *ast.StructLit:
		return structLooksLikeMapOfLists(e)
	default:
		return false
	}
}

func isMapOfListsExpr(expr ast.Expr, mapOfListsNames map[string]bool) bool {
	expr = unwrapParenExpr(expr)
	switch e := expr.(type) {
	case *ast.Ident:
		return mapOfListsNames[e.Name]
	case *ast.SelectorExpr:
		key := selectorPath(e)
		return key != "" && mapOfListsNames[key]
	case *ast.IndexExpr:
		return isMapOfListsExpr(e.X, mapOfListsNames)
	default:
		return false
	}
}

func selectorPath(sel *ast.SelectorExpr) string {
	var parts []string
	cur := ast.Expr(sel)
	for {
		s, ok := cur.(*ast.SelectorExpr)
		if !ok {
			break
		}
		id, ok := s.Sel.(*ast.Ident)
		if !ok {
			return ""
		}
		parts = append([]string{id.Name}, parts...)
		cur = s.X
	}
	root, ok := cur.(*ast.Ident)
	if !ok {
		return ""
	}
	parts = append([]string{root.Name}, parts...)
	return strings.Join(parts, ".")
}

func buildConcatCall(operands []ast.Expr) ast.Expr {
	return &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("list"), Sel: ast.NewIdent("Concat")}, Args: []ast.Expr{&ast.ListLit{Elts: operands}}}
}
