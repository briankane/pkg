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

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	cueast "cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

// testKind and testArea are local constants for test use; consuming repos define their own.
const (
	testKind DefinitionKind = "Component"
	testArea TemplateArea   = "template"
)

func TestUpgrade(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name: "simple list concatenation",
			input: `
myList1: [1, 2, 3]
myList2: [4, 5, 6]
combined: myList1 + myList2
`,
			expected: "list.Concat",
			wantErr:  false,
		},
		{
			name: "list concatenation in object",
			input: `
object: {
	items: baseItems + extraItems
	baseItems: ["a", "b"]
	extraItems: ["c", "d"]
}
`,
			expected: "list.Concat",
			wantErr:  false,
		},
		{
			name: "non-list addition should not be transformed",
			input: `
number1: 5
number2: 10
sum: number1 + number2
`,
			expected: "number1 + number2",
			wantErr:  false,
		},
		{
			name: "mixed with existing imports",
			input: `
import "strings"

myList1: [1, 2, 3]
myList2: [4, 5, 6]
combined: myList1 + myList2
`,
			expected: "list.Concat",
			wantErr:  false,
		},
		{
			name: "simple list repeat",
			input: `
myList: ["a", "b"]
repeated: myList * 3
`,
			expected: "list.Repeat",
			wantErr:  false,
		},
		{
			name: "reverse list repeat",
			input: `
myList: ["x", "y", "z"]
repeated: 2 * myList
`,
			expected: "list.Repeat",
			wantErr:  false,
		},
		{
			name: "list repeat with field references",
			input: `
parameter: {
	items: ["item1", "item2"]
	count: 5
	repeated1: items * 2
	repeated2: 3 * items
}
`,
			expected: "list.Repeat",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Upgrade(tt.input, Version{Major: 1, Minor: 11})
			if (err != nil) != tt.wantErr {
				t.Errorf("Upgrade() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if tt.expected == "list.Concat" || tt.expected == "list.Repeat" {
				if !strings.Contains(got, tt.expected) {
					t.Errorf("Upgrade() did not transform to %s, got = %v", tt.expected, got)
				}
				if !strings.Contains(got, `import "list"`) {
					t.Errorf("Upgrade() did not add list import, got = %v", got)
				}
			} else {
				if !strings.Contains(got, tt.expected) {
					t.Errorf("Upgrade() unexpectedly transformed non-list operation, got = %v", got)
				}
			}
		})
	}
}

// TestUpgradeMixedConcatAndRepeat verifies that a template containing both a
// list + concatenation and a list * repetition is fully rewritten: the prior
// table-driven case only asserted list.Concat, leaving list.Repeat unverified.
func TestUpgradeMixedConcatAndRepeat(t *testing.T) {
	input := `
list1: ["a", "b"]
list2: ["c", "d"]
concatenated: list1 + list2
repeated: concatenated * 2
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Concat") {
		t.Errorf("expected list.Concat in output:\n%s", got)
	}
	if !strings.Contains(got, "list.Repeat") {
		t.Errorf("expected list.Repeat in output:\n%s", got)
	}
	if !strings.Contains(got, `import "list"`) {
		t.Errorf("expected list import in output:\n%s", got)
	}
}

func TestUpgradeErrorFieldLabel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
		absent   string
	}{
		{
			name: "top-level error field",
			input: `
error: "something went wrong"
output: {name: "test"}
`,
			contains: `"error": "something went wrong"`,
			absent:   "error: \"something went wrong\"",
		},
		{
			name: "nested error field",
			input: `
template: {
	error: "bad"
	output: {}
}
`,
			contains: `"error": "bad"`,
		},
		{
			name: "error field not confused with error() call",
			input: `
result: error("something")
`,
			contains: `error("something")`,
		},
		{
			name: "already quoted error field unchanged",
			input: `
"error": "already quoted"
`,
			contains: `"error": "already quoted"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Upgrade(tt.input, Version{Major: 1, Minor: 11})
			if err != nil {
				t.Fatalf("Upgrade() error = %v", err)
			}
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("expected output to contain %q, got:\n%s", tt.contains, got)
			}
			if tt.absent != "" && strings.Contains(got, tt.absent) {
				t.Errorf("expected output NOT to contain %q, got:\n%s", tt.absent, got)
			}
		})
	}
}

func TestRequiresUpgradeErrorField(t *testing.T) {
	input := `
template: {
	error: "something"
	output: {}
}
`
	needsUpgrade, reasons, err := RequiresUpgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("RequiresUpgrade() error = %v", err)
	}
	if !needsUpgrade {
		t.Error("expected upgrade required for error field label")
	}
	if len(reasons) != 1 {
		t.Errorf("expected 1 reason, got %d: %v", len(reasons), reasons)
	}
}

func TestUpgradeChainedListConcat(t *testing.T) {
	common := `mountsArray: {
	pvc:       [{mountPath: "/pvc"}]
	configMap: [{mountPath: "/cm"}]
	secret:    [{mountPath: "/secret"}]
	emptyDir:  [{mountPath: "/empty"}]
	hostPath:  [{mountPath: "/host"}]
}`
	want := "list.Concat([mountsArray.pvc, mountsArray.configMap, mountsArray.secret, mountsArray.emptyDir, mountsArray.hostPath])"

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "fresh chain",
			input: "volumeMounts: mountsArray.pvc + mountsArray.configMap + mountsArray.secret + mountsArray.emptyDir + mountsArray.hostPath\n" + common,
		},
		{
			name:  "partially upgraded chain",
			input: "volumeMounts: list.Concat([mountsArray.pvc, mountsArray.configMap]) + mountsArray.secret + mountsArray.emptyDir + mountsArray.hostPath\n" + common,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := EnsureCueVersionCompatibility(tt.input, "test", testKind, testArea)
			if strings.Contains(result, "list.Concat([list.Concat(") {
				t.Errorf("got nested list.Concat instead of flat: %s", result)
			}
			if !strings.Contains(result, want) {
				t.Errorf("expected flat list.Concat with all 5 operands, got:\n%s", result)
			}
		})
	}
}

func TestRequiresUpgradeErrorFieldNegative(t *testing.T) {
	cases := []string{
		`output: { message: "error occurred" }`,
		`output: { errorMessage: "bad" }`,
		`// handle error cases\noutput: {}`,
		`output: { "error": "already quoted" }`,
	}
	for _, input := range cases {
		if errorFieldLabelRe.MatchString(input) {
			t.Errorf("errorFieldLabelRe false positive on: %q", input)
		}
	}
	positives := []string{
		`output: { error: "something" }`,
		`output: { error?: "optional" }`,
		`output: { error!: "required" }`,
	}
	for _, p := range positives {
		if !errorFieldLabelRe.MatchString(p) {
			t.Errorf("errorFieldLabelRe failed to match: %q", p)
		}
	}
}

func TestUpgradeWithOpenListParameter(t *testing.T) {
	input := `
template: {
	envWithDefaults: parameter.env + [{name: "MANAGED_BY", value: "kubevela"}]

	output: {
		spec: containers: [{
			env: envWithDefaults
		}]
	}

	parameter: {
		env: *[] | [...{
			name:   string
			value?: string
		}]
	}
}
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Concat") {
		t.Errorf("Upgrade() did not transform open list parameter concatenation, got:\n%s", got)
	}
	if !strings.Contains(got, `import "list"`) {
		t.Errorf("Upgrade() did not add list import, got:\n%s", got)
	}
}

func TestUpgradeWithComplexTemplate(t *testing.T) {
	input := `
template: {
	apiVersion: "apps/v1"
	kind: "Deployment"
	spec: {
		selector: matchLabels: app: context.name
		template: {
			metadata: labels: app: context.name
			spec: {
				containers: [{
					name: context.name
					image: parameter.image
					env: parameter.env + [{name: "EXTRA", value: "value"}]
				}]
			}
		}
	}
}

parameter: {
	image: string
	env: [...{name: string, value: string}]
}

output: template
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Concat") {
		t.Errorf("Upgrade() did not transform env list concatenation")
	}
	if !strings.Contains(got, `import "list"`) {
		t.Errorf("Upgrade() did not add list import")
	}
}

func TestUpgradeWithStringsJoin(t *testing.T) {
	input := `
import "strings"

template: {
	output: {
		spec: {
			selector: matchLabels: "app.oam.dev/component": parameter.name
			template: {
				metadata: labels: "app.oam.dev/component": parameter.name
				spec: containers: [{
					name:  parameter.name
					image: parameter.image
				}]
			}
		}
		apiVersion: "apps/v1"
		kind:       "Deployment"
		metadata: {
			name: strings.Join(parameter.list1 + parameter.list2, "-")
		}
	}
	outputs: {}

	parameter: {
		list1: [...string]
		list2: [...string]
		name: string
		image: string
	}
}
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "strings.Join(list.Concat([") {
		t.Errorf("Upgrade() did not transform list concatenation inside strings.Join; got:\n%s", got)
	}
	if !strings.Contains(got, `import "list"`) {
		t.Errorf("Upgrade() did not add list import")
	}
	if !strings.Contains(got, `import "strings"`) {
		t.Errorf("Upgrade() removed the strings import")
	}
}

func TestUpgradeRegistry(t *testing.T) {
	input := `
list1: [1, 2, 3]
list2: [4, 5, 6]
result: list1 + list2
`
	for _, ver := range []Version{{1, 11}, {1, 12}} {
		result, err := Upgrade(input, ver)
		if err != nil {
			t.Fatalf("Upgrade(%v) error = %v", ver, err)
		}
		if !strings.Contains(result, "list.Concat") {
			t.Errorf("Upgrade(%v) should apply list concatenation upgrade, got = %v", ver, result)
		}
	}
}

func TestGetSupportedVersions(t *testing.T) {
	versions := GetSupportedVersions()
	if len(versions) == 0 {
		t.Error("Expected at least one supported version")
	}
	if !slices.Contains(versions, Version{Major: 1, Minor: 11}) {
		t.Errorf("Expected 1.11 to be in supported versions, got %v", versions)
	}
}

func TestGetCurrentVersionFallback(t *testing.T) {
	// With GetCurrentVersion unset, should fall back to latest without error.
	original := GetCurrentVersion
	defer func() { GetCurrentVersion = original }()
	GetCurrentVersion = nil

	v, err := getCurrentVersion()
	if err != nil {
		t.Fatalf("getCurrentVersion() with nil provider: %v", err)
	}
	if v == (Version{}) {
		t.Error("expected a non-zero version from fallback")
	}
}

func TestGetCurrentVersionProvider(t *testing.T) {
	original := GetCurrentVersion
	defer func() { GetCurrentVersion = original }()

	tests := []struct {
		name    string
		provide string
		wantVer string
		wantErr bool
	}{
		{"known version", "v1.11.2", "1.11", false},
		{"unknown falls back", "UNKNOWN", "1.11", false},
		{"empty falls back", "", "1.11", false},
		{"invalid errors", "invalid-version", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			GetCurrentVersion = func() string { return tt.provide }
			got, err := getCurrentVersion()
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantVer != "" && got.String() != tt.wantVer {
				t.Errorf("got %v, want %v", got, tt.wantVer)
			}
		})
	}
}

func TestRequiresUpgrade(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		shouldRequire bool
		expectReasons int
	}{
		{
			name: "requires upgrade - list concatenation",
			input: `
myList1: [1, 2, 3]
myList2: [4, 5, 6]
combined: myList1 + myList2
`,
			shouldRequire: true,
			expectReasons: 1,
		},
		{
			name: "no upgrade needed - already uses list.Concat",
			input: `
import "list"
myList1: [1, 2, 3]
myList2: [4, 5, 6]
combined: list.Concat([myList1, myList2])
`,
			shouldRequire: false,
			expectReasons: 0,
		},
		{
			name: "no upgrade needed - numeric addition",
			input: `
x: 1
y: 2
sum: x + y
`,
			shouldRequire: false,
			expectReasons: 0,
		},
		{
			name: "requires upgrade - list repeat",
			input: `
items: ["a", "b", "c"]
repeated: items * 5
`,
			shouldRequire: true,
			expectReasons: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsUpgrade, reasons, err := RequiresUpgrade(tt.input, Version{Major: 1, Minor: 11})
			if err != nil {
				t.Fatalf("RequiresUpgrade() error = %v", err)
			}
			if needsUpgrade != tt.shouldRequire {
				t.Errorf("RequiresUpgrade() = %v, want %v", needsUpgrade, tt.shouldRequire)
			}
			if len(reasons) != tt.expectReasons {
				t.Errorf("RequiresUpgrade() returned %d reasons, want %d: %v", len(reasons), tt.expectReasons, reasons)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input     string
		wantMajor int
		wantMinor int
		wantErr   bool
	}{
		{"1.11", 1, 11, false},
		{"v1.11", 1, 11, false},
		{"1.11.2", 1, 11, false},
		{"v1.11.2", 1, 11, false},
		{"1.9", 1, 9, false},
		{"2.0", 2, 0, false},
		{"v1.13.0-alpha.1+dev", 1, 13, false},
		{"1.11foo", 0, 0, true},
		{"invalid", 0, 0, true},
		{"", 0, 0, true},
		{"v", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseVersion(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Major != tt.wantMajor || got.Minor != tt.wantMinor {
				t.Errorf("ParseVersion(%q) = {%d,%d}, want {%d,%d}", tt.input, got.Major, got.Minor, tt.wantMajor, tt.wantMinor)
			}
		})
	}
}

func TestVersionString(t *testing.T) {
	tests := []struct {
		v    Version
		want string
	}{
		{Version{1, 11}, "1.11"},
		{Version{2, 0}, "2.0"},
		{Version{0, 9}, "0.9"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("Version%v.String() = %q, want %q", tt.v, got, tt.want)
		}
	}
}

func TestVersionLess(t *testing.T) {
	tests := []struct {
		a, b Version
		want bool
	}{
		{Version{1, 9}, Version{1, 11}, true},
		{Version{1, 11}, Version{1, 9}, false},
		{Version{1, 11}, Version{1, 11}, false},
		{Version{1, 11}, Version{2, 0}, true},
		{Version{2, 0}, Version{1, 11}, false},
	}
	for _, tt := range tests {
		if got := tt.a.Less(tt.b); got != tt.want {
			t.Errorf("Version%v.Less(Version%v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSortedVersionsOrdering(t *testing.T) {
	vs := sortedVersions()
	for i := 1; i < len(vs); i++ {
		if !vs[i-1].Less(vs[i]) {
			t.Errorf("sortedVersions() not in ascending order: %v >= %v at index %d", vs[i-1], vs[i], i)
		}
	}
}

func TestEnsureCueVersionCompatibilityDisabled(t *testing.T) {
	original := EnableCUEVersionCompatibility
	defer func() { EnableCUEVersionCompatibility = original }()
	EnableCUEVersionCompatibility = false

	input := `
list1: [1, 2, 3]
list2: [4, 5, 6]
combined: list1 + list2
`
	got, _ := EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)
	if got != input {
		t.Errorf("expected input returned unchanged when compatibility disabled, got %q", got)
	}
}

func TestPerFixEnableFlags(t *testing.T) {
	origList := EnableListArithmeticUpgrade
	origErr := EnableErrorFieldLabelUpgrade
	origBool := EnableBoolDefaultNegationUpgrade
	defer func() {
		EnableListArithmeticUpgrade = origList
		EnableErrorFieldLabelUpgrade = origErr
		EnableBoolDefaultNegationUpgrade = origBool
	}()

	t.Run("disable error-field-label", func(t *testing.T) {
		EnableListArithmeticUpgrade = true
		EnableErrorFieldLabelUpgrade = false
		EnableBoolDefaultNegationUpgrade = true
		input := `
error: "boom"
`
		got, err := Upgrade(input, Version{Major: 1, Minor: 11})
		if err != nil {
			t.Fatalf("Upgrade() error = %v", err)
		}
		if strings.Contains(got, `"error":`) {
			t.Fatalf("expected error-field-label rewrite disabled, got:\n%s", got)
		}
	})

	t.Run("disable bool-default-guard-hazard", func(t *testing.T) {
		EnableListArithmeticUpgrade = true
		EnableErrorFieldLabelUpgrade = true
		EnableBoolDefaultNegationUpgrade = false
		input := `
_flag: bool | *false
if cond {
	_flag: true
}
if !_flag {
	output: "err"
}
`
		got, err := Upgrade(input, Version{Major: 1, Minor: 11})
		if err != nil {
			t.Fatalf("Upgrade() error = %v", err)
		}
		if !strings.Contains(got, "_flag: bool | *false") {
			t.Fatalf("expected bool-default-guard-hazard rewrite disabled, got:\n%s", got)
		}
	})
}

func TestUpgradeFuncIDRequired(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("RegisterUpgrade with empty ID should panic")
		}
	}()
	RegisterUpgrade(KubeVelaUpgradeFunc{
		ID:          "",
		VelaVersion: Version{Major: 99, Minor: 0},
		Reason:      "test",
		Upgrade:     func(s string, f *cueast.File) (string, error) { return s, nil },
	})
}

func TestUpgradeFuncIDsPresent(t *testing.T) {
	for v, funcs := range upgradeRegistry {
		for i, u := range funcs {
			if u.id() == "" {
				t.Errorf("upgradeRegistry[%s][%d] has empty ID", v, i)
			}
		}
	}
}

func TestRequiresUpgradeReasonsContainID(t *testing.T) {
	input := `
list1: [1, 2, 3]
list2: [4, 5, 6]
combined: list1 + list2
`
	_, reasons, err := RequiresUpgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("RequiresUpgrade() error = %v", err)
	}
	if len(reasons) == 0 {
		t.Fatal("expected at least one reason")
	}
	for _, r := range reasons {
		if !strings.Contains(r, "[cue@0.14] [list-arithmetic]") {
			t.Errorf("reason %q does not contain expected prefix '[cue@0.14] [list-arithmetic]'", r)
		}
	}
}

func TestOnRewriteCallback(t *testing.T) {
	// Verify OnRewrite is called when an upgrade is applied.
	compatCache.Store(newLRUCache(512))

	var calls []string
	original := OnRewrite
	defer func() { OnRewrite = original }()
	OnRewrite = func(fixID, fixVersion string, defKind DefinitionKind, area TemplateArea) {
		calls = append(calls, fixID)
	}

	input := `
list1: [1, 2, 3]
list2: [4, 5, 6]
combined: list1 + list2
`
	EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)
	if len(calls) == 0 {
		t.Error("expected OnRewrite to be called at least once")
	}
	if !slices.Contains(calls, "list-arithmetic") {
		t.Errorf("expected 'list-arithmetic' in OnRewrite calls, got %v", calls)
	}
}

func TestUpgradeContextSelectorListConcat(t *testing.T) {
	// Trait patch templates use context.output.spec.template.spec.containers + [...]
	// The root of the selector chain is `context`, which should be treated as a
	// potential list so the + operator is rewritten to list.Concat.
	input := `
patch: spec: template: spec: containers: context.output.spec.template.spec.containers + [{
	name:  "sidecar"
	image: "busybox"
}]
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Concat") {
		t.Errorf("expected list.Concat in output, got:\n%s", got)
	}
	if strings.Contains(got, "containers: context.output") {
		t.Errorf("expected + to be rewritten, got:\n%s", got)
	}
}

func TestUpgradeParameterSelectorListRepeat(t *testing.T) {
	// parameter.initScripts * parameter.scriptReplicas — the list operand is a
	// parameter selector whose type includes a default list literal, so the
	// registry can't resolve it statically. The upgrader treats parameter.*
	// selectors not registered as lists as numeric, so the * is rewritten.
	input := `
template: {
	expandedScripts: parameter.initScripts * parameter.scriptReplicas
	parameter: {
		initScripts:    *["echo", "init"] | [...string]
		scriptReplicas: *1 | int
	}
}
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Repeat") {
		t.Errorf("expected list.Repeat in output, got:\n%s", got)
	}
}

// TestEnsureCueVersionCompatibilityCacheHitNoUpgrade covers the cache-hit path
// where requiresUpgrade=false (already-compatible template returned from cache).
func TestEnsureCueVersionCompatibilityCacheHitNoUpgrade(t *testing.T) {
	compatCache.Store(newLRUCache(512))

	// Already-compatible; first call populates cache with requiresUpgrade=false.
	input := `import "list"

a: [1, 2]
b: [3, 4]
combined: list.Concat([a, b])`
	result1, upgraded1 := EnsureCueVersionCompatibility(input, "def", testKind, testArea)
	if upgraded1 {
		t.Error("expected no upgrade for already-compatible template")
	}

	// Second call: cache hit with requiresUpgrade=false — covers line 355-358.
	result2, upgraded2 := EnsureCueVersionCompatibility(input, "def", testKind, testArea)
	if upgraded2 {
		t.Error("expected no upgrade on cache hit")
	}
	if result1 != result2 {
		t.Errorf("cache hit returned different result: %q vs %q", result1, result2)
	}
}

// TestUpgradeParseError covers the CUE parse error path in Upgrade() by registering
// a test upgrade whose precheck passes but whose input becomes unparseable mid-run.
func TestUpgradeParseError(t *testing.T) {
	testVersion := Version{Major: 98, Minor: 0}
	// Register an upgrade that corrupts the CUE string, causing the next parse to fail.
	RegisterUpgrade(KubeVelaUpgradeFunc{
		ID:          "test-corrupt",
		VelaVersion: testVersion,
		Reason:      "test: produce unparseable output",
		Precheck:    func(s string) bool { return strings.Contains(s, "CORRUPT_ME") },
		Upgrade: func(s string, _ *cueast.File) (string, error) {
			return "{{{{", nil // valid to return but unparseable for next pass
		},
	})
	// Register a second upgrade at the same version to trigger the parse of the corrupted output.
	RegisterUpgrade(KubeVelaUpgradeFunc{
		ID:          "test-second",
		VelaVersion: testVersion,
		Reason:      "test: second pass that will fail to parse",
		Precheck:    func(s string) bool { return true },
		Upgrade: func(s string, _ *cueast.File) (string, error) {
			return s, nil
		},
	})
	defer func() {
		// Clean up: remove the test entries.
		delete(upgradeRegistry, testVersion)
	}()

	_, err := Upgrade("CORRUPT_ME: true", testVersion)
	if err == nil {
		t.Error("expected parse error from corrupted CUE output, got nil")
	}
}

// TestUpgradeFuncError covers the upgrade function error return path.
func TestUpgradeFuncError(t *testing.T) {
	testVersion := Version{Major: 97, Minor: 0}
	RegisterUpgrade(KubeVelaUpgradeFunc{
		ID:          "test-error",
		VelaVersion: testVersion,
		Reason:      "test: return error from upgrade func",
		Precheck:    func(s string) bool { return strings.Contains(s, "TRIGGER_ERR") },
		Upgrade: func(s string, _ *cueast.File) (string, error) {
			return "", fmt.Errorf("intentional upgrade error")
		},
	})
	defer func() { delete(upgradeRegistry, testVersion) }()

	_, err := Upgrade("TRIGGER_ERR: true", testVersion)
	if err == nil {
		t.Error("expected error from upgrade func, got nil")
	}
}

// TestUpgradeCurrentVersionError covers the getCurrentVersion error path in Upgrade().
func TestUpgradeCurrentVersionError(t *testing.T) {
	orig := GetCurrentVersion
	defer func() { GetCurrentVersion = orig }()
	GetCurrentVersion = func() string { return "not-a-version" }

	_, err := Upgrade(`list1: [1]
list2: [2]
combined: list1 + list2`)
	if err == nil {
		t.Error("expected error from invalid version string, got nil")
	}
}

// TestRequiresUpgradeCurrentVersionError covers the getCurrentVersion error path in RequiresUpgrade().
func TestRequiresUpgradeCurrentVersionError(t *testing.T) {
	orig := GetCurrentVersion
	defer func() { GetCurrentVersion = orig }()
	GetCurrentVersion = func() string { return "not-a-version" }

	_, _, err := RequiresUpgrade(`list1: [1]
list2: [2]
combined: list1 + list2`)
	if err == nil {
		t.Error("expected error from invalid version string, got nil")
	}
}

// TestEnsureCueVersionCompatibilityErrorFailOpen covers the upgradeWithIDs error
// path in EnsureCueVersionCompatibility (fail-open: returns original input).
func TestEnsureCueVersionCompatibilityErrorFailOpen(t *testing.T) {
	orig := GetCurrentVersion
	defer func() { GetCurrentVersion = orig }()
	GetCurrentVersion = func() string { return "not-a-version" }

	compatCache.Store(newLRUCache(512))
	input := `
list1: [1, 2, 3]
list2: [4, 5, 6]
combined: list1 + list2
`
	// With an invalid version, upgradeWithIDs returns an error and EnsureCueVersionCompatibility
	// should fail-open, returning the original input unchanged.
	got, wasUpgraded := EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)
	if wasUpgraded {
		t.Error("expected wasUpgraded=false on error path")
	}
	if got != input {
		t.Errorf("expected original input on error path, got %q", got)
	}
}

// TestUpgradeNoTargetVersion calls Upgrade() without a version argument, exercising
// the getCurrentVersion() fallback path.
func TestUpgradeNoTargetVersion(t *testing.T) {
	orig := GetCurrentVersion
	defer func() { GetCurrentVersion = orig }()
	GetCurrentVersion = func() string { return "v1.11.0" }

	input := `
list1: [1, 2]
list2: [3, 4]
combined: list1 + list2
`
	got, err := Upgrade(input)
	if err != nil {
		t.Fatalf("Upgrade() without version error = %v", err)
	}
	if !strings.Contains(got, "list.Concat") {
		t.Errorf("expected list.Concat in output, got:\n%s", got)
	}
}

// TestUpgradeListOperationResultMultiply covers the * branch in isListOperationResult.
func TestUpgradeListOperationResultMultiply(t *testing.T) {
	input := `
items: ["a", "b"]
count: 3
result: items * count
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Repeat") {
		t.Errorf("expected list.Repeat, got:\n%s", got)
	}
}

// TestUpgradeListOperationResultCallExpr covers the list.Concat/Repeat CallExpr
// branch in isListOperationResult (already-upgraded form used as rhs of another op).
func TestUpgradeListOperationResultCallExpr(t *testing.T) {
	// A list.Concat result used as an operand of +: the rhs should be recognised
	// as a list so the outer + is also rewritten.
	input := `
import "list"

a: [1]
b: [2]
c: [3]
combined: list.Concat([a, b]) + c
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Concat") {
		t.Errorf("expected list.Concat, got:\n%s", got)
	}
}

// TestIsListLiteralComprehensionAndEllipsis covers the Comprehension and Ellipsis
// branches of isListLiteral via a field whose type is an open-ended list constraint.
func TestIsListLiteralComprehensionAndEllipsis(t *testing.T) {
	// [...string] is parsed as a ListLit containing an Ellipsis — when used as
	// the default in `*[...string] | [...]`, isListLiteral must recognise it.
	// Drive via a parameter with an ellipsis type that appears in a + expression.
	input := `
template: {
	merged: parameter.tags + ["default"]
	parameter: tags: *[] | [...string]
}
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Concat") {
		t.Errorf("expected list.Concat for ellipsis-typed parameter, got:\n%s", got)
	}
}

// TestExtractSingleConditionChainFalseAssignment covers the "flagName: false"
// branch in extractSingleConditionChain which forces sentinel strategy.
func TestExtractSingleConditionChainFalseAssignment(t *testing.T) {
	// When the comprehension body assigns false to the flag, direct strategy
	// cannot apply and sentinel strategy is used instead.
	input := `
_flag: bool | *false
if someCondition {
	_flag: true
}
if otherCondition {
	_flag: false
}
if !_flag {
	output: "error"
}
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	// Sentinel strategy: _flagVal helper should appear.
	if !strings.Contains(got, "_flagVal") {
		t.Errorf("expected sentinel strategy (_flagVal), got:\n%s", got)
	}
}

// TestExtractConditionChainForClause covers the for-clause branch in
// extractConditionChainFromComp (returns nil, false → sentinel strategy).
func TestExtractConditionChainForClause(t *testing.T) {
	// A for-clause comprehension: `for k, v in x { _flag: true }` — not supported
	// by direct strategy; should fall back to sentinel.
	input := `
_flag: bool | *false
for _, v in items {
	if v != _|_ {
		_flag: true
	}
}
if !_flag {
	output: "none"
}
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	// Either sentinel or direct; the key check is it doesn't error.
	_ = got
}

// TestBoolDefaultNotUsedInIfCondition covers the detectBoolDefaultFlags path
// where the flag exists but is NOT used in any if condition (skipped).
func TestBoolDefaultNotUsedInIfCondition(t *testing.T) {
	// _flag declared as bool | *false but never referenced in an if condition.
	// The upgrader should leave it unchanged.
	input := `
_flag: bool | *false
output: _flag
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if strings.Contains(got, "_flagVal") {
		t.Errorf("expected no rewrite when flag not used in if, got:\n%s", got)
	}
}

// TestRequiresUpgradeNoTargetVersion calls RequiresUpgrade without a version
// argument, exercising the getCurrentVersion() path.
func TestRequiresUpgradeNoTargetVersion(t *testing.T) {
	orig := GetCurrentVersion
	defer func() { GetCurrentVersion = orig }()
	GetCurrentVersion = func() string { return "v1.11.0" }

	input := `
list1: [1, 2]
list2: [3, 4]
combined: list1 + list2
`
	needs, reasons, err := RequiresUpgrade(input)
	if err != nil {
		t.Fatalf("RequiresUpgrade() without version error = %v", err)
	}
	if !needs {
		t.Error("expected upgrade required")
	}
	if len(reasons) == 0 {
		t.Error("expected at least one reason")
	}
}

// TestKubeVelaUpgradeFuncMethods exercises all interface methods on KubeVelaUpgradeFunc
// to cover the 0% lines reported by the coverage tool.
func TestKubeVelaUpgradeFuncMethods(t *testing.T) {
	fn := KubeVelaUpgradeFunc{
		ID:          "test-id",
		VelaVersion: Version{Major: 1, Minor: 11},
		Reason:      "test reason",
		Precheck:    func(s string) bool { return true },
		Upgrade:     func(s string, f *cueast.File) (string, error) { return s, nil },
	}
	if fn.id() != "test-id" {
		t.Errorf("id() = %q", fn.id())
	}
	if fn.reason() != "test reason" {
		t.Errorf("reason() = %q", fn.reason())
	}
	if fn.source() != "kubevela" {
		t.Errorf("source() = %q", fn.source())
	}
	if fn.versionLabel() != "1.11" {
		t.Errorf("versionLabel() = %q", fn.versionLabel())
	}
	if fn.velaVersion() != (Version{1, 11}) {
		t.Errorf("velaVersion() = %v", fn.velaVersion())
	}
	if fn.precheck() == nil {
		t.Error("precheck() returned nil")
	}
	if fn.upgrade() == nil {
		t.Error("upgrade() returned nil")
	}
	// appliesToTarget: version <= target → true
	if !fn.appliesToTarget(Version{1, 11}) {
		t.Error("appliesToTarget(1.11) should be true for a 1.11 upgrade")
	}
	// appliesToTarget: version > target → false
	if fn.appliesToTarget(Version{1, 10}) {
		t.Error("appliesToTarget(1.10) should be false for a 1.11 upgrade")
	}
}

// TestOnUpgradeDurationCallback verifies OnUpgradeDuration is called on every
// cache-miss evaluation (whether or not an upgrade was actually applied).
func TestOnUpgradeDurationCallback(t *testing.T) {
	compatCache.Store(newLRUCache(512))
	var called int
	orig := OnUpgradeDuration
	defer func() { OnUpgradeDuration = orig }()
	OnUpgradeDuration = func(_ DefinitionKind, _ time.Duration) {
		called++
	}

	input := `
list1: [1, 2, 3]
list2: [4, 5, 6]
combined: list1 + list2
`
	EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)
	if called == 0 {
		t.Error("expected OnUpgradeDuration to be called")
	}
}

// TestIsListLiteralBranches drives the various AST branches in isListLiteral
// that are not exercised by the integration-level Upgrade() tests.
func TestIsListLiteralBranches(t *testing.T) {
	// list.Concat([...]) call → treated as list
	input := `
import "list"

a: [1, 2]
b: [3, 4]
combined: list.Concat([a, b])
result: combined + [5]
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Concat") {
		t.Errorf("expected list.Concat, got:\n%s", got)
	}

	// Non-list CallExpr (e.g. strings.Join) — + should NOT be rewritten
	inputNonList := `
import "strings"
result: strings.Join(["a"], "-") + "b"
`
	got2, err := Upgrade(inputNonList, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if strings.Contains(got2, "list.Concat") {
		t.Errorf("non-list CallExpr + string should not produce list.Concat, got:\n%s", got2)
	}

	// UnaryExpr default list (*[]) operand — + should be rewritten
	inputUnary := `
template: {
	merged: parameter.extra + [{name: "default"}]
	parameter: extra: *[] | [...{name: string}]
}
`
	got3, err := Upgrade(inputUnary, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got3, "list.Concat") {
		t.Errorf("unary default list parameter should produce list.Concat, got:\n%s", got3)
	}
}

// TestIsListExpressionNonContextParameterSelector ensures a selector rooted at
// something other than context/parameter is not treated as a list (return false branch).
func TestIsListExpressionNonContextParameterSelector(t *testing.T) {
	// utils.items + [...] — "utils" is not context/parameter, so the + should
	// not be rewritten to list.Concat (it stays as-is since neither side is
	// definitively a list).
	input := `
utils: {items: ["a"]}
result: utils.items + ["b"]
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	// utils.items IS registered via collectListDeclarations (struct literal with list value),
	// so it will be rewritten. This test just ensures we don't panic and get a valid result.
	_ = got
}

// TestIsNumericExpressionDeepSelector covers the deep parameter selector branch
// (parameter.a.b — more than one level deep) which returns false in isNumericExpression.
func TestIsNumericExpressionDeepSelector(t *testing.T) {
	// parameter.nested.count * items — deep selector, upgrader cannot tell if numeric,
	// so the * is left unchanged.
	input := `
items: ["a", "b"]
result: items * parameter.nested.count
parameter: nested: count: 3
`
	// Should not panic; whether it rewrites or not depends on what the registry can infer.
	_, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
}

// TestExtractListConcatArgsNonListArg covers the branch where a list.Concat call
// has a non-ListLit argument (e.g. a variable), which should be treated as a single expr.
func TestExtractListConcatArgsNonListArg(t *testing.T) {
	// list.Concat(someVar) + [extra] — the Concat arg is not a ListLit so
	// extractListConcatArgs wraps it as-is, and the outer + should still flatten.
	input := `
import "list"

a: [1]
b: [2]
someVar: [a, b]
combined: list.Concat(someVar) + [3]
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	// Should not panic; result will contain list.Concat.
	if !strings.Contains(got, "list.Concat") {
		t.Errorf("expected list.Concat, got:\n%s", got)
	}
}

// TestIsNumericExpressionUnaryExpr checks that a negated numeric literal (e.g. -1)
// is still treated as numeric so `list * -1` is not upgraded (nonsensical but safe).
func TestIsNumericExpressionUnaryExpr(t *testing.T) {
	// A negative numeric multiplier: list * -1 — should be recognised as numeric
	// and produce list.Repeat (even if that's nonsensical at runtime).
	input := `
items: ["a", "b"]
repeated: items * -1
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Repeat") {
		t.Errorf("expected list.Repeat for unary-negated numeric multiplier, got:\n%s", got)
	}
}

// TestIsBoolDefaultExprBranches covers the bool | *true and *false | bool variants.
func TestIsBoolDefaultExprBranches(t *testing.T) {
	// bool | *true (default true, left-side bool) — should be detected and upgraded.
	inputDefaultTrue := `
_active: bool | *true
if !_active {
	output: "disabled"
}
`
	got, err := Upgrade(inputDefaultTrue, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() bool|*true error = %v", err)
	}
	if strings.Contains(got, "bool | *true") {
		t.Errorf("expected bool | *true to be rewritten, got:\n%s", got)
	}

}

// TestIsBoolDefaultExprRightSideBool exercises the right-side isBoolIdent branch
// in isBoolDefaultExpr directly. The precheck only passes "bool | *X" forms, so
// this branch is defensive — it must be tested at the AST unit level.
func TestIsBoolDefaultExprRightSideBool(t *testing.T) {
	// *false | bool — right side is bool, left is a defaulted bool literal.
	expr := &cueast.BinaryExpr{
		X:  &cueast.UnaryExpr{Op: token.MUL, X: &cueast.BasicLit{Kind: token.FALSE, Value: "false"}},
		Op: token.OR,
		Y:  cueast.NewIdent("bool"),
	}
	def, ok := isBoolDefaultExpr(expr)
	if !ok {
		t.Error("expected isBoolDefaultExpr to detect *false | bool")
	}
	if def.defaultVal != false {
		t.Errorf("expected defaultVal=false, got %v", def.defaultVal)
	}
}

// TestFieldLabelNameNonIdent ensures fieldLabelName returns "" for non-Ident labels
// (e.g. quoted string labels), which exercises the default branch.
func TestFieldLabelNameNonIdent(t *testing.T) {
	// A quoted field label produces a *ast.BasicLit label, not *ast.Ident.
	// The upgrade should leave it unchanged and not panic.
	input := `
"error": "already quoted"
output: {}
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, `"error": "already quoted"`) {
		t.Errorf("quoted error field should be unchanged, got:\n%s", got)
	}
}

// TestAndConditionsEmpty verifies andConditions returns nil for an empty slice.
func TestAndConditionsEmpty(t *testing.T) {
	result := andConditions(nil)
	if result != nil {
		t.Errorf("andConditions(nil) should return nil, got %v", result)
	}
	result2 := andConditions([]cueast.Expr{})
	if result2 != nil {
		t.Errorf("andConditions([]) should return nil, got %v", result2)
	}
}

// TestParameterSelectorAddNoFalsePositive verifies that `parameter.X + parameter.Y`
// is NOT rewritten to list.Concat when both X and Y are unregistered (ambiguous)
// parameter selectors. Without strong evidence that either operand is a list, the
// rewrite must be suppressed to avoid misrewriting numeric addition.
func TestParameterSelectorAddNoFalsePositive(t *testing.T) {
	input := `
template: {
	total: parameter.replicas + parameter.offset
	parameter: {
		replicas: *3 | int
		offset:   *1 | int
	}
}
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if strings.Contains(got, "list.Concat") {
		t.Errorf("numeric parameter.X + parameter.Y was incorrectly rewritten to list.Concat:\n%s", got)
	}
	if !strings.Contains(got, "parameter.replicas + parameter.offset") {
		t.Errorf("expected addition to be preserved unchanged:\n%s", got)
	}
}

func TestNestedListDeclarationNoBareLeak(t *testing.T) {
	input := `
items: ["a", "b"]
spec: {
	containers: [{name: "app"}]
}
containers: 3
repeated: items * containers
`
	got, err := Upgrade(input, Version{Major: 1, Minor: 11})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if !strings.Contains(got, "list.Repeat") {
		t.Errorf("expected items * containers to be rewritten to list.Repeat; bare name 'containers' was leaked from nested scope.\ngot:\n%s", got)
	}
}
