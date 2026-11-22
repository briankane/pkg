/*
Copyright 2026 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cuex_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"cuelang.org/go/cue"
	"github.com/stretchr/testify/require"

	"github.com/kubevela/pkg/apis/cue/v1alpha1"
	"github.com/kubevela/pkg/cue/cuex"
	cuexruntime "github.com/kubevela/pkg/cue/cuex/runtime"
)

// fromPackage is a provider as a Package would make it, with restrictions or
// none.
type fromPackage struct {
	cuexruntime.Package
	src *v1alpha1.Package
}

func (p fromPackage) Source() *v1alpha1.Package { return p.src }

func (p fromPackage) GroupsOf(string) []string { return nil }

// echoCompiler has one function, echo.#Say, from a Package, and counts its calls.
func echoCompiler(t *testing.T, restricted bool, calls *atomic.Int32) *cuex.Compiler {
	t.Helper()
	fn := cuexruntime.Concurrent(cuexruntime.GenericProviderFn[waitParams, waitReturns](
		func(_ context.Context, p *waitParams) (*waitReturns, error) {
			calls.Add(1)
			return &waitReturns{Returns: p.Params}, nil
		}))
	pkg, err := cuexruntime.NewInternalPackage("echo", `
package echo

#Say: {
	#do:       "say"
	#provider: "echo"
	#config: maxPerRender: 64
	$params:   string
	$returns?: string
}`, map[string]cuexruntime.ProviderFn{"say": fn})
	require.NoError(t, err)
	src := &v1alpha1.Package{}
	src.Namespace, src.Name = "vela-system", "echo"
	if restricted {
		src.Spec.Restrictions = &v1alpha1.PackageRestrictions{CallRule: v1alpha1.CallRule{Namespaces: []string{"vela-system"}}}
	}
	// stored as the informer stores a Package, unwrapped
	c := cuex.NewCompilerWithInternalPackages()
	c.Externals.Set("external://vela-system/echo", fromPackage{Package: pkg, src: src})
	return c
}

func says(n int, attr string) string {
	if attr == "loop" {
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprint(i)
		}
		return "import \"vela/echo\"\nout: {for i in [" + strings.Join(items, ", ") + "] {\"\\(i)\": echo.#Say & {$params: \"hi\"}}}\n"
	}
	var b strings.Builder
	b.WriteString("import \"vela/echo\"\nout: {\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\t\"%d\": echo.#Say & {$params: \"hi\"}\n", i)
	}
	b.WriteString("}" + attr + "\n")
	return b.String()
}

// prepass turns on answering loops before the resolver for the loop path, which
// makes its calls from a path of its own.
func prepass(attr string) []cuex.CompileOption {
	if attr != "loop" {
		return nil
	}
	return []cuex.CompileOption{cuex.WithOptimise(cuex.OptimisePolicy{Enabled: true, Threshold: 1, LoneThreshold: 1})}
}

func TestCallCheck(t *testing.T) {
	for _, path := range []struct{ name, attr string }{{"one at a time", ""}, {"together", " @concurrency(4)"}, {"from a loop, answered before resolving", "loop"}} {
		t.Run(path.name, func(t *testing.T) {
			t.Run("an unrestricted provider runs with no check installed", func(t *testing.T) {
				var calls atomic.Int32
				_, err := echoCompiler(t, false, &calls).CompileStringWithOptions(context.Background(), says(3, path.attr), prepass(path.attr)...)
				require.NoError(t, err)
				require.Equal(t, int32(3), calls.Load())
			})
			t.Run("a restricted provider is refused with no check installed", func(t *testing.T) {
				var calls atomic.Int32
				_, err := echoCompiler(t, true, &calls).CompileStringWithOptions(context.Background(), says(3, path.attr), prepass(path.attr)...)
				require.ErrorContains(t, err, "vela-system/echo is restricted, and nothing here checks who may call it")
				require.Zero(t, calls.Load())
			})
			t.Run("the check sees the call, the provider and the owner, before the provider", func(t *testing.T) {
				var calls atomic.Int32
				c := echoCompiler(t, true, &calls)
				var mu sync.Mutex
				var seen []string
				c.CallCheck = func(ctx context.Context, provider cuexruntime.Provider, fn string, call cue.Value) error {
					owner, _ := cuex.OwnerFrom(ctx)
					params, _ := call.LookupPath(cue.ParsePath("$params")).String()
					mu.Lock()
					seen = append(seen, owner.Name+" "+provider.(fromPackage).src.Name+"#"+fn+" "+params)
					mu.Unlock()
					return errors.New("not today")
				}
				ctx := cuex.WithOwner(context.Background(), cuex.Owner{Kind: "ComponentDefinition", Namespace: "team-a", Name: "web"})
				_, err := c.CompileStringWithOptions(ctx, says(3, path.attr), prepass(path.attr)...)
				require.ErrorContains(t, err, "not today")
				require.Zero(t, calls.Load(), "a refused call never reaches its provider")
				require.NotEmpty(t, seen)
				require.Equal(t, "web echo#say hi", seen[0])
			})
			t.Run("a check that allows lets every call run", func(t *testing.T) {
				var calls atomic.Int32
				c := echoCompiler(t, true, &calls)
				var checked atomic.Int32
				c.CallCheck = func(context.Context, cuexruntime.Provider, string, cue.Value) error {
					checked.Add(1)
					return nil
				}
				_, err := c.CompileStringWithOptions(context.Background(), says(3, path.attr), prepass(path.attr)...)
				require.NoError(t, err)
				require.Equal(t, int32(3), calls.Load())
				require.Equal(t, int32(3), checked.Load())
			})
		})
	}
}
