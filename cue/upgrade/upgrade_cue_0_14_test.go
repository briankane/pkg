/*
Copyright 2024 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and limitations under the License.
*/

package upgrade

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueparser "cuelang.org/go/cue/parser"
)

func TestUpgradeBoolDefaultNegation(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		absent   []string
	}{
		{
			// Direct strategy: two nested ifs → inlined AND, no default, if-blocks removed.
			name: "direct: exact PDF reproduction",
			input: `
parameter: {
	globalCluster?: {
		mode: string
	}
	engineVersion?: string
}

_isGlobalSecondary: bool | *false
if parameter.globalCluster != _|_ {
	if parameter.globalCluster.mode == "secondary" {
		_isGlobalSecondary: true
	}
}
if parameter.globalCluster != _|_ {
	if !_isGlobalSecondary {
		if parameter.engineVersion == _|_ {
			_error: 0 & "BUG: secondary wrongly required engineVersion"
		}
	}
}
`,
			contains: []string{
				`parameter.globalCluster != _|_ && parameter.globalCluster.mode == "secondary"`,
				`if !_isGlobalSecondary`,
			},
			absent: []string{
				`_isGlobalSecondary: bool | *false`,
				`_isGlobalSecondary: true`,
				`_isGlobalSecondaryVal`,
				// No default on rewritten field - that's the whole fix
				`| *false`,
			},
		},
		{
			// Direct strategy: single-level condition.
			name: "direct: single condition",
			input: `
_isPrimary: bool | *false
if parameter.mode == "primary" {
	_isPrimary: true
}
if !_isPrimary {
	_error: 0 & "must be primary"
}
`,
			contains: []string{
				`parameter.mode == "primary"`,
				`if !_isPrimary`,
			},
			absent: []string{
				`_isPrimary: bool | *false`,
				`_isPrimary: true`,
				`_isPrimaryVal`,
				`| *false`,
			},
		},
		{
			// Direct strategy: three levels of nesting → all three conditions ANDed.
			name: "direct: deeply nested assignment",
			input: `
_flag: bool | *false
if parameter.a != _|_ {
	if parameter.a.b != _|_ {
		if parameter.a.b.c == "x" {
			_flag: true
		}
	}
}
if !_flag {
	_error: 0 & "flag must be set"
}
`,
			contains: []string{
				`parameter.a != _|_`,
				`parameter.a.b != _|_`,
				`parameter.a.b.c == "x"`,
				`if !_flag`,
			},
			absent: []string{
				`_flag: bool | *false`,
				`_flag: true`,
				`_flagVal`,
				`| *false`,
			},
		},
		{
			// Fallback sentinel: bool | *true with false-setting block can't be
			// inverted simply, so falls back to sentinel. The helper must default
			// to "true" (not "") so the field is enabled when the condition is unmet.
			name: "fallback: bool | *true variant",
			input: `
_isEnabled: bool | *true
if parameter.disabled {
	_isEnabled: false
}
if _isEnabled {
	output: "active"
}
`,
			contains: []string{
				`_isEnabledVal`,
				`*"true" | string`,
				`_isEnabledVal == "true"`,
				`if _isEnabled`,
			},
			absent: []string{
				`_isEnabled: bool | *true`,
				`_isEnabled: false`,
				`*"" | string`,
			},
		},
		{
			// Fallback sentinel: two separate if-blocks set the flag.
			name: "fallback: multiple setting blocks",
			input: `
_flag: bool | *false
if conditionA {
	_flag: true
}
if conditionB {
	_flag: true
}
if !_flag {
	_error: 0 & "required"
}
`,
			contains: []string{
				`_flagVal`,
				`*"" | string`,
				`_flagVal == "true"`,
				`if !_flag`,
			},
			absent: []string{
				`_flag: bool | *false`,
			},
		},
		{
			// Fallback sentinel: body has more than one element alongside assignment.
			name: "fallback: multi-element body",
			input: `
_flag: bool | *false
if someCondition {
	_flag: true
	_other: "set"
}
if !_flag {
	_error: 0 & "required"
}
`,
			contains: []string{
				`_flagVal`,
				`_flagVal == "true"`,
				`if !_flag`,
			},
			absent: []string{
				`_flag: bool | *false`,
			},
		},
		{
			// No-op: flag not used in an if-guard.
			name: "noop: flag not used in if-condition",
			input: `
_unused: bool | *false
if someCondition {
	_unused: true
}
output: _unused
`,
			contains: []string{
				`_unused: bool | *false`,
			},
			absent: []string{
				`_unusedVal`,
			},
		},
		{
			// No-op: non-bool fields untouched.
			name: "noop: non-bool fields untouched",
			input: `
_mode: *"" | string
if parameter.foo {
	_mode: "bar"
}
`,
			contains: []string{
				`_mode: *"" | string`,
			},
			absent: []string{
				`_modeVal`,
			},
		},
		{
			// Direct strategy: two independent flags, each with a single clean chain.
			name: "direct: multiple flags in same scope",
			input: `
_flagA: bool | *false
_flagB: bool | *false
if condA {
	_flagA: true
}
if condB {
	_flagB: true
}
if !_flagA {
	_error: 0 & "A required"
}
if !_flagB {
	_error: 0 & "B required"
}
`,
			contains: []string{
				`condA`,
				`condB`,
				`if !_flagA`,
				`if !_flagB`,
			},
			absent: []string{
				`_flagA: bool | *false`,
				`_flagB: bool | *false`,
				`_flagAVal`,
				`_flagBVal`,
				`| *false`,
			},
		},
		{
			// Direct strategy inside a nested struct scope.
			name: "direct: flag inside nested struct scope",
			input: `
template: {
	_isPrimary: bool | *false
	if parameter.mode == "primary" {
		_isPrimary: true
	}
	if !_isPrimary {
		_error: 0 & "must be primary"
	}
}
`,
			contains: []string{
				`parameter.mode == "primary"`,
				`if !_isPrimary`,
			},
			absent: []string{
				`_isPrimary: bool | *false`,
				`_isPrimary: true`,
				`_isPrimaryVal`,
				`| *false`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Upgrade(tt.input, Version{Major: 1, Minor: 11})
			if err != nil {
				t.Fatalf("Upgrade() error = %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("expected output to contain %q\ngot:\n%s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("expected output NOT to contain %q\ngot:\n%s", absent, got)
				}
			}
		})
	}
}

func TestRequiresUpgradeBoolDefault(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		shouldRequire bool
	}{
		{
			name: "detects bool default negation",
			input: `
_isGlobalSecondary: bool | *false
if parameter.globalCluster.mode == "secondary" {
	_isGlobalSecondary: true
}
if !_isGlobalSecondary {
	_error: 0 & "required"
}
`,
			shouldRequire: true,
		},
		{
			name: "already fixed with direct expression - no default",
			input: `
_isGlobalSecondary: parameter.globalCluster != _|_ && parameter.globalCluster.mode == "secondary"
if !_isGlobalSecondary {
	_error: 0 & "required"
}
`,
			shouldRequire: false,
		},
		{
			name: "already fixed with sentinel helper",
			input: `
_gcMode: *"" | string
if parameter.globalCluster != _|_ {
	_gcMode: parameter.globalCluster.mode
}
_isGlobalSecondary: _gcMode == "secondary"
if !_isGlobalSecondary {
	_error: 0 & "required"
}
`,
			shouldRequire: false,
		},
		{
			name: "bool default not used in if-guard - no upgrade needed",
			input: `
_flag: bool | *false
if cond {
	_flag: true
}
output: _flag
`,
			shouldRequire: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsUpgrade, reasons, err := RequiresUpgrade(tt.input, Version{Major: 1, Minor: 11})
			if err != nil {
				t.Fatalf("RequiresUpgrade() error = %v", err)
			}
			if needsUpgrade != tt.shouldRequire {
				t.Errorf("RequiresUpgrade() = %v, want %v; reasons: %v", needsUpgrade, tt.shouldRequire, reasons)
			}
		})
	}
}

// TestBoolDefaultNegationEndToEnd verifies that after the upgrade is applied,
// the resulting CUE evaluates correctly when parameter values are injected -
// mirroring the full KubeVela render pipeline.
//
// The template uses three layers of nested if conditions to set _flag:
//
//	Layer 1: parameter.a must be present
//	Layer 2: parameter.a.b must be present
//	Layer 3: parameter.a.b.c must equal "yes"
//
// A guard fires an _error when _flag is false and parameter.extra is absent.
func TestBoolDefaultNegationEndToEnd(t *testing.T) {
	const template = `
parameter: {
	a?: {
		b?: {
			c?: string
		}
	}
	extra?: string
}

_flag: bool | *false
if parameter.a != _|_ {
	if parameter.a.b != _|_ {
		if parameter.a.b.c == "yes" {
			_flag: true
		}
	}
}

if parameter.a != _|_ {
	if !_flag {
		if parameter.extra == _|_ {
			_error: 0 & "extra is required when flag is not set"
		}
	}
}
`

	tests := []struct {
		name      string
		injected  string
		wantFlag  bool
		wantError bool
	}{
		{
			// All three layers satisfied: _flag true, guard skipped.
			name: "all conditions met - flag true, no error",
			injected: `parameter: {
	a: { b: { c: "yes" } }
}`,
			wantFlag:  true,
			wantError: false,
		},
		{
			// Layer 3 fails (c != "yes"): _flag false, guard fires, extra absent → error.
			name: "layer 3 fails - flag false, error",
			injected: `parameter: {
	a: { b: { c: "no" } }
}`,
			wantFlag:  false,
			wantError: true,
		},
		{
			// Layer 3 fails but extra provided: _flag false, guard fires but passes.
			name: "layer 3 fails, extra provided - flag false, no error",
			injected: `parameter: {
	a:     { b: { c: "no" } }
	extra: "supplied"
}`,
			wantFlag:  false,
			wantError: false,
		},
		{
			// Layer 2 fails (b absent): _flag false, guard fires, extra absent → error.
			name: "layer 2 fails - flag false, error",
			injected: `parameter: {
	a: {}
}`,
			wantFlag:  false,
			wantError: true,
		},
		{
			// Layer 1 fails (a absent): outer gate blocks guard entirely, no error.
			// _flag stays non-concrete since it references an absent optional field.
			name:      "layer 1 fails - no error",
			injected:  `parameter: {}`,
			wantFlag:  false,
			wantError: false,
		},
	}

	// Apply the upgrade once - all cases share the rewritten template.
	upgraded, err := Upgrade(template, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if strings.Contains(upgraded, "bool | *false") {
		t.Fatalf("upgrade did not rewrite bool | *false pattern:\n%s", upgraded)
	}

	ctx := cuecontext.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			combined := tt.injected + "\n" + upgraded

			f, err := cueparser.ParseFile("", combined, cueparser.ParseComments)
			if err != nil {
				t.Fatalf("ParseFile() error = %v", err)
			}
			v := ctx.BuildFile(f)

			evalErr := v.Err()
			if tt.wantError && evalErr == nil {
				t.Errorf("expected evaluation error, got none\nrewritten template:\n%s", upgraded)
			}
			if !tt.wantError && evalErr != nil {
				t.Errorf("unexpected evaluation error: %v\nrewritten template:\n%s", evalErr, upgraded)
			}
			if evalErr != nil {
				return
			}

			// When a is absent _flag references an optional field and stays
			// non-concrete - acceptable as long as no error fired.
			flagVal := v.LookupPath(cue.MakePath(cue.Hid("_flag", "_")))
			if gotFlag, flagErr := flagVal.Bool(); flagErr == nil {
				if gotFlag != tt.wantFlag {
					t.Errorf("_flag = %v, want %v\nrewritten template:\n%s", gotFlag, tt.wantFlag, upgraded)
				}
			}
		})
	}
}

// TestBoolDefaultNegationTrueDefaultEndToEnd verifies the sentinel fallback path for
// bool | *true fields. The default is true (enabled), and a conditional block sets it
// to false (disabled). After rewrite, the helper must default to "true" so that the
// flag evaluates to true when the disabling condition is not met.
func TestBoolDefaultNegationTrueDefaultEndToEnd(t *testing.T) {
	const template = `
parameter: {
	disabled?: bool
	extra?:    string
}

_isEnabled: bool | *true
if parameter.disabled {
	_isEnabled: false
}

if _isEnabled {
	output: "active"
}
if !_isEnabled {
	if parameter.extra == _|_ {
		_error: 0 & "extra required when disabled"
	}
}
`
	upgraded, err := Upgrade(template, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if strings.Contains(upgraded, "bool | *true") {
		t.Fatalf("upgrade did not rewrite bool | *true pattern:\n%s", upgraded)
	}
	if !strings.Contains(upgraded, `*"true" | string`) {
		t.Fatalf("expected helper to default to \"true\", got:\n%s", upgraded)
	}

	tests := []struct {
		name        string
		injected    string
		wantEnabled bool
		wantError   bool
	}{
		{
			name:        "disabled not set - enabled by default, no error",
			injected:    `parameter: {}`,
			wantEnabled: true,
			wantError:   false,
		},
		{
			name:        "disabled true - flag false, extra absent → error",
			injected:    `parameter: { disabled: true }`,
			wantEnabled: false,
			wantError:   true,
		},
		{
			name:        "disabled true, extra provided - flag false, no error",
			injected:    `parameter: { disabled: true, extra: "ok" }`,
			wantEnabled: false,
			wantError:   false,
		},
		{
			name:        "disabled false - flag true, no error",
			injected:    `parameter: { disabled: false }`,
			wantEnabled: true,
			wantError:   false,
		},
	}

	ctx := cuecontext.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			combined := tt.injected + "\n" + upgraded
			f, err := cueparser.ParseFile("", combined, cueparser.ParseComments)
			if err != nil {
				t.Fatalf("ParseFile() error = %v", err)
			}
			v := ctx.BuildFile(f)

			evalErr := v.Err()
			if tt.wantError && evalErr == nil {
				t.Errorf("expected evaluation error, got none\nrewritten template:\n%s", upgraded)
			}
			if !tt.wantError && evalErr != nil {
				t.Errorf("unexpected evaluation error: %v\nrewritten template:\n%s", evalErr, upgraded)
			}
			if evalErr != nil {
				return
			}

			flagVal := v.LookupPath(cue.MakePath(cue.Hid("_isEnabled", "_")))
			if gotEnabled, flagErr := flagVal.Bool(); flagErr == nil {
				if gotEnabled != tt.wantEnabled {
					t.Errorf("_isEnabled = %v, want %v\nrewritten template:\n%s", gotEnabled, tt.wantEnabled, upgraded)
				}
			}
		})
	}
}

// TestBoolDefaultNegationSentinelEndToEnd verifies the sentinel fallback path
// end-to-end. The sentinel is used when the direct strategy cannot apply -
// here because two separate if-blocks set the flag (multiple setting blocks).
//
// The template sets _flag from two independent conditions, so the direct
// strategy (which requires exactly one setting block) falls back to the
// sentinel string helper.
func TestBoolDefaultNegationSentinelEndToEnd(t *testing.T) {
	const template = `
parameter: {
	condA?: string
	condB?: string
	extra?: string
}

_flag: bool | *false
if parameter.condA == "yes" {
	_flag: true
}
if parameter.condB == "yes" {
	_flag: true
}

if !_flag {
	if parameter.extra == _|_ {
		_error: 0 & "extra required when neither condA nor condB is yes"
	}
}
`

	tests := []struct {
		name      string
		injected  string
		wantFlag  bool
		wantError bool
	}{
		{
			name:      "condA yes - flag true, no error",
			injected:  `parameter: { condA: "yes" }`,
			wantFlag:  true,
			wantError: false,
		},
		{
			name:      "condB yes - flag true, no error",
			injected:  `parameter: { condB: "yes" }`,
			wantFlag:  true,
			wantError: false,
		},
		{
			name:      "both yes - flag true, no error",
			injected:  `parameter: { condA: "yes", condB: "yes" }`,
			wantFlag:  true,
			wantError: false,
		},
		{
			name:      "neither yes, no extra - flag false, error",
			injected:  `parameter: { condA: "no", condB: "no" }`,
			wantFlag:  false,
			wantError: true,
		},
		{
			name:      "neither yes, extra provided - flag false, no error",
			injected:  `parameter: { condA: "no", condB: "no", extra: "supplied" }`,
			wantFlag:  false,
			wantError: false,
		},
	}

	upgraded, err := Upgrade(template, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	// Sentinel path: bool | *false gone, helper var present.
	if strings.Contains(upgraded, "bool | *false") {
		t.Fatalf("upgrade did not rewrite bool | *false:\n%s", upgraded)
	}
	if !strings.Contains(upgraded, "_flagVal") {
		t.Fatalf("expected sentinel helper _flagVal in output:\n%s", upgraded)
	}

	ctx := cuecontext.New()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			combined := tt.injected + "\n" + upgraded

			f, err := cueparser.ParseFile("", combined, cueparser.ParseComments)
			if err != nil {
				t.Fatalf("ParseFile() error = %v", err)
			}
			v := ctx.BuildFile(f)

			evalErr := v.Err()
			if tt.wantError && evalErr == nil {
				t.Errorf("expected evaluation error, got none\nrewritten template:\n%s", upgraded)
			}
			if !tt.wantError && evalErr != nil {
				t.Errorf("unexpected evaluation error: %v\nrewritten template:\n%s", evalErr, upgraded)
			}
			if evalErr != nil {
				return
			}

			flagVal := v.LookupPath(cue.MakePath(cue.Hid("_flag", "_")))
			if gotFlag, flagErr := flagVal.Bool(); flagErr == nil {
				if gotFlag != tt.wantFlag {
					t.Errorf("_flag = %v, want %v\nrewritten template:\n%s", gotFlag, tt.wantFlag, upgraded)
				}
			}
		})
	}
}
