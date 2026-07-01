/*
Copyright 2024 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package upgrade

// All three fixes in this file address breaking changes introduced in CUE v0.14.
// They shipped with KubeVela v1.11, which was the first release to require CUE >= v0.14.
//
// Fix 1 — list-arithmetic
//
//	List concatenation via + and repetition via * became hard errors in CUE v0.14.
//	Rewrite: list1 + list2  →  list.Concat([list1, list2])
//	         list * n       →  list.Repeat(list, n)
//
// Fix 2 — error-field-label
//
//	The `error` built-in was introduced in CUE v0.14; unquoted `error:` field
//	labels now conflict with it.
//	Rewrite: error: "msg"  →  "error": "msg"
//
// Fix 3 — bool-default-negation
//
//	In CUE v0.14+ the evaluator reads the default value of a `bool | *false`
//	field when evaluating `if !_flag`, before conditional assignments are unified.
//	This causes the guard to fire incorrectly for cases where _flag should be true.
//
//	Primary rewrite (direct expression): when the flag is set by exactly one
//	chain of nested ifs, the conditions are ANDed and inlined:
//	    _flag: (condA && condB) | *false
//	The original if-blocks that set the flag are removed.
//
//	Fallback rewrite (sentinel string): for more complex patterns:
//	    _flagVal: *"" | string
//	    if cond { _flagVal: "true" }
//	    _flag: _flagVal == "true"

import (
	"fmt"
	"regexp"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/token"
)

var v1_11 = Version{Major: 1, Minor: 11}

// errorFieldLabelRe matches an unquoted `error` used as a field label.
var errorFieldLabelRe = regexp.MustCompile(`\berror\s*[?!]?\s*:`)

func init() {
	RegisterUpgrade(CUEUpgradeFunc{
		ID:                    "list-arithmetic",
		CUEVersion:            Version{0, 14},
		AssociatedVelaVersion: v1_11,
		Reason:                "contains deprecated list operators (+ or *) that need upgrading to list.Concat() or list.Repeat()",
		Precheck:              func(s string) bool { return strings.Contains(s, "+") || strings.Contains(s, "*") },
		Upgrade:               upgradeListConcatenation,
	})

	RegisterUpgrade(CUEUpgradeFunc{
		ID:                    "error-field-label",
		CUEVersion:            Version{0, 14},
		AssociatedVelaVersion: v1_11,
		Reason:                `contains field named 'error' which conflicts with the CUE 0.14 built-in; must be quoted as "error"`,
		Precheck:              func(s string) bool { return errorFieldLabelRe.MatchString(s) },
		Upgrade:               upgradeErrorFieldLabel,
	})

	RegisterUpgrade(CUEUpgradeFunc{
		ID:                    "bool-default-negation",
		CUEVersion:            Version{0, 14},
		AssociatedVelaVersion: v1_11,
		Reason:                "contains a `bool | *false` or `bool | *true` field used in an if-guard; CUE v0.14+ reads the default before unification, causing incorrect evaluation",
		Precheck: func(s string) bool {
			return (strings.Contains(s, "bool | *false") || strings.Contains(s, "bool | *true")) &&
				strings.Contains(s, "if ")
		},
		Upgrade: upgradeBoolDefaultNegation,
	})
}

// -----------------------------------------------------------------------------
// Fix 1: list-arithmetic
// -----------------------------------------------------------------------------

func upgradeListConcatenation(cueStr string, file *ast.File) (string, error) {
	transformed := upgradeListConcatenationAST(file)
	result, err := format.Node(transformed)
	if err != nil {
		return "", fmt.Errorf("failed to format CUE: %w", err)
	}
	return strings.TrimRight(string(result), "\n"), nil
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

// collectAddChain flattens a left-associative + chain into operands, returning
// nil if any operand is not a list. Already-upgraded list.Concat([...]) calls
// are inlined so the result is always a single flat list.Concat.
func collectAddChain(expr ast.Expr, listRegistry map[string]bool) []ast.Expr {
	operands, hasStrong := collectAddChainInner(expr, listRegistry)
	if operands == nil || !hasStrong {
		return nil
	}
	return operands
}

// collectAddChainInner returns the flattened operand list and whether any operand
// is a statically-known (strong) list.
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

// isStrongListExpression returns true for expressions that are statically known
// to be lists: list literals, list.* calls, and registry-registered list
// variables. It returns false for context.*/parameter.* selectors that are not
// registered in the list registry, which are ambiguous at static analysis time.
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
		if base, ok := e.X.(*ast.Ident); ok {
			if sel, ok := e.Sel.(*ast.Ident); ok {
				// Check both the qualified name (e.g. "parameter.list1") and the
				// bare selector name (e.g. "list1"), since nested list declarations
				// are registered under both their qualified path and their bare name.
				return listRegistry[base.Name+"."+sel.Name] || listRegistry[sel.Name]
			}
		}
		return false
	}
	return false
}

// extractListConcatArgs unwraps a list.Concat([...]) call to its inner
// elements, or wraps expr in a single-element slice if it is not such a call.
func extractListConcatArgs(expr ast.Expr) []ast.Expr {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return []ast.Expr{expr}
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return []ast.Expr{expr}
	}
	base, ok := sel.X.(*ast.Ident)
	if !ok || base.Name != "list" {
		return []ast.Expr{expr}
	}
	selName, ok := sel.Sel.(*ast.Ident)
	if !ok || selName.Name != "Concat" {
		return []ast.Expr{expr}
	}
	listLit, ok := call.Args[0].(*ast.ListLit)
	if !ok {
		return []ast.Expr{expr}
	}
	return listLit.Elts
}

func ensureListImport(file *ast.File) {
	for _, imp := range file.Imports {
		if imp.Path != nil && imp.Path.Value == "\"list\"" {
			return
		}
	}
	for _, decl := range file.Decls {
		if importDecl, ok := decl.(*ast.ImportDecl); ok {
			for _, spec := range importDecl.Specs {
				if spec.Path != nil && spec.Path.Value == "\"list\"" {
					return
				}
			}
		}
	}
	listImport := &ast.ImportSpec{
		Path: &ast.BasicLit{Kind: token.STRING, Value: "\"list\""},
	}
	file.Imports = append([]*ast.ImportSpec{listImport}, file.Imports...)
	file.Decls = append([]ast.Decl{&ast.ImportDecl{Specs: []*ast.ImportSpec{listImport}}}, file.Decls...)
}

func collectListDeclarations(file *ast.File) map[string]bool {
	listRegistry := make(map[string]bool)
	// nonListTopLevel tracks top-level field names that are definitively not lists.
	// After nested passes, any bare-name entry leaked from a nested struct that
	// collides with a top-level non-list name is removed to prevent misclassification.
	nonListTopLevel := make(map[string]bool)

	// First pass: iterate only top-level decls (not astutil.Apply) so we do not
	// visit nested fields directly and accidentally register their bare names.
	for _, decl := range file.Decls {
		field, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		label, ok := field.Label.(*ast.Ident)
		if !ok {
			continue
		}
		if isListLiteral(field.Value) {
			listRegistry[label.Name] = true
		} else if structLit, ok := field.Value.(*ast.StructLit); ok {
			nonListTopLevel[label.Name] = true
			collectNestedListDeclarationsFirstPass(structLit, label.Name, listRegistry)
		} else {
			nonListTopLevel[label.Name] = true
		}
	}

	topLevelListNames := make(map[string]bool)

	changed := true
	for changed {
		changed = false
		for _, decl := range file.Decls {
			field, ok := decl.(*ast.Field)
			if !ok {
				continue
			}
			label, ok := field.Label.(*ast.Ident)
			if !ok {
				continue
			}
			if !listRegistry[label.Name] && isListOperationResult(field.Value, listRegistry) {
				listRegistry[label.Name] = true
				topLevelListNames[label.Name] = true
				changed = true
			} else if structLit, ok := field.Value.(*ast.StructLit); ok {
				if collectNestedListDeclarationsSecondPass(structLit, label.Name, listRegistry) {
					changed = true
				}
			}
		}
	}

	// Remove bare-name entries that were leaked from nested scopes but conflict
	// with a top-level field that is not a list. Skip names confirmed as lists
	// by the top-level second pass.
	for name := range nonListTopLevel {
		if !topLevelListNames[name] {
			delete(listRegistry, name)
		}
	}

	return listRegistry
}

func collectNestedListDeclarationsFirstPass(structLit *ast.StructLit, prefix string, listRegistry map[string]bool) {
	for _, elt := range structLit.Elts {
		field, ok := elt.(*ast.Field)
		if !ok {
			continue
		}
		label, ok := field.Label.(*ast.Ident)
		if !ok {
			continue
		}
		qualifiedName := prefix + "." + label.Name
		if isListLiteral(field.Value) {
			listRegistry[qualifiedName] = true
			listRegistry[label.Name] = true
		} else if nestedStruct, ok := field.Value.(*ast.StructLit); ok {
			collectNestedListDeclarationsFirstPass(nestedStruct, qualifiedName, listRegistry)
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
		label, ok := field.Label.(*ast.Ident)
		if !ok {
			continue
		}
		qualifiedName := prefix + "." + label.Name
		if !listRegistry[qualifiedName] && isListOperationResult(field.Value, listRegistry) {
			listRegistry[qualifiedName] = true
			listRegistry[label.Name] = true
			changed = true
		} else if nestedStruct, ok := field.Value.(*ast.StructLit); ok {
			if collectNestedListDeclarationsSecondPass(nestedStruct, qualifiedName, listRegistry) {
				changed = true
			}
		}
	}
	return changed
}

func isListLiteral(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.ListLit:
		return true
	case *ast.Comprehension:
		return true
	case *ast.Ellipsis:
		return true
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "list" {
				return true
			}
		}
		return false
	case *ast.BinaryExpr:
		if e.Op.String() == "|" {
			return isListLiteral(e.X) || isListLiteral(e.Y)
		}
		return false
	case *ast.UnaryExpr:
		return isListLiteral(e.X)
	}
	return false
}

func isListExpression(expr ast.Expr, listRegistry map[string]bool) bool {
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
		if base, ok := e.X.(*ast.Ident); ok {
			if sel, ok := e.Sel.(*ast.Ident); ok {
				if listRegistry[base.Name+"."+sel.Name] {
					return true
				}
			}
		}
		root := selectorRootIdent(e)
		return root == "context" || root == "parameter"
	}
	return false
}

// selectorRootIdent walks a SelectorExpr chain and returns the root Ident name.
func selectorRootIdent(e *ast.SelectorExpr) string {
	cur := ast.Expr(e)
	for {
		sel, ok := cur.(*ast.SelectorExpr)
		if !ok {
			break
		}
		cur = sel.X
	}
	if id, ok := cur.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func isNumericExpression(expr ast.Expr, listRegistry map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.INT || e.Kind == token.FLOAT
	case *ast.Ident:
		return !listRegistry[e.Name]
	case *ast.UnaryExpr:
		return isNumericExpression(e.X, listRegistry)
	case *ast.SelectorExpr:
		if selectorRootIdent(e) == "parameter" {
			if base, ok := e.X.(*ast.Ident); ok {
				if sel, ok := e.Sel.(*ast.Ident); ok {
					return !listRegistry[base.Name+"."+sel.Name]
				}
			}
			return false
		}
		return false
	}
	return false
}

func isListOperationResult(expr ast.Expr, listRegistry map[string]bool) bool {
	if binExpr, ok := expr.(*ast.BinaryExpr); ok {
		if binExpr.Op.String() == "+" {
			// Require at least one strongly-known list operand, mirroring collectAddChain.
			return isListExpression(binExpr.X, listRegistry) && isListExpression(binExpr.Y, listRegistry) &&
				(isStrongListExpression(binExpr.X, listRegistry) || isStrongListExpression(binExpr.Y, listRegistry))
		}
		if binExpr.Op.String() == "*" {
			// Require the list side to be strongly-known, mirroring the rewrite step.
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

// -----------------------------------------------------------------------------
// Fix 2: error-field-label
// -----------------------------------------------------------------------------

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

// -----------------------------------------------------------------------------
// Fix 3: bool-default-negation
// -----------------------------------------------------------------------------

func upgradeBoolDefaultNegation(cueStr string, file *ast.File) (string, error) {
	rewritten := rewriteBoolDefaultFlags(file)
	result, err := format.Node(rewritten)
	if err != nil {
		return "", fmt.Errorf("failed to format CUE after bool-default-negation rewrite: %w", err)
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
			return nil, false // for-clauses not supported
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
