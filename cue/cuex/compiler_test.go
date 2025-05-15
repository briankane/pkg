/*
Copyright 2023 The KubeVela Authors.

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
	"cuelang.org/go/pkg/strings"
	"fmt"
	"github.com/kubevela/pkg/util/singleton"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	kuberuntime "k8s.io/apimachinery/pkg/runtime"

	"github.com/kubevela/pkg/cue/cuex"
	cuexruntime "github.com/kubevela/pkg/cue/cuex/runtime"
	"github.com/kubevela/pkg/cue/util"
	"github.com/kubevela/pkg/util/runtime"
)

func TestAddFlags(t *testing.T) {
	set := pflag.NewFlagSet("-", 0)
	cuex.AddFlags(set)
}

func TestCompile(t *testing.T) {
	compiler := cuex.NewCompilerWithDefaultInternalPackages()
	ctx := context.Background()
	str := `
		import "vela/base64"
		parameter: input: string
		_enc: base64.#Encode & { $params: parameter.input }
		output: _enc.$returns
	`
	val, err := compiler.CompileStringWithOptions(ctx, str, cuex.WithExtraData("parameter", map[string]interface{}{
		"input": "example",
	}))
	require.NoError(t, err)
	s, err := val.LookupPath(cue.ParsePath("output")).String()
	require.NoError(t, err)
	require.Equal(t, "ZXhhbXBsZQ==", s)

	val, err = compiler.CompileStringWithOptions(ctx, str, cuex.WithExtraData("parameter", &kuberuntime.RawExtension{Raw: []byte(`{"input": "example"}`)}))
	require.NoError(t, err)
	s, err = val.LookupPath(cue.ParsePath("output")).String()
	require.NoError(t, err)
	require.Equal(t, "ZXhhbXBsZQ==", s)

	var test *kuberuntime.RawExtension
	val, err = compiler.CompileStringWithOptions(ctx, `a: parameter`, cuex.WithExtraData("parameter", test))
	require.NoError(t, err)
	s, err = util.ToString(val)
	require.NoError(t, err)
	require.Equal(t, "a: {}\nparameter: {}", s)

	val, err = compiler.CompileStringWithOptions(ctx, str, cuex.DisableResolveProviderFunctions{})
	require.NoError(t, err)
	_, err = val.LookupPath(cue.ParsePath("output")).String()
	require.Error(t, err)

	val, err = compiler.CompileStringWithOptions(ctx, `a: parameter.nested`, cuex.WithExtraData("parameter.nested", "value"))
	require.NoError(t, err)
	s, err = val.LookupPath(cue.ParsePath("a")).String()
	require.NoError(t, err)
	require.Equal(t, "value", s)
}

func TestResolve(t *testing.T) {
	compiler := &cuex.Compiler{
		PackageManager: cuexruntime.NewPackageManager(
			cuexruntime.WithInternalPackage{
				Package: runtime.Must(cuexruntime.NewInternalPackage("test", "", map[string]cuexruntime.ProviderFn{
					"err": cuexruntime.GenericProviderFn[int, int](func(ctx context.Context, t *int) (*int, error) {
						return nil, fmt.Errorf("err")
					}),
					"timeout": cuexruntime.GenericProviderFn[int, int](func(ctx context.Context, t *int) (*int, error) {
						time.Sleep(time.Second)
						return t, nil
					}),
				})),
			}),
	}
	ctx := context.Background()
	cctx := cuecontext.New()

	for name, tt := range map[string]struct {
		Input string
		Error error
	}{
		"provider-not-found": {
			Input: `x: {
				#do: "fn"
				#provider: "unknown"
			}`,
			Error: cuex.ProviderNotFoundErr("unknown"),
		},
		"provider-fn-not-found": {
			Input: `x: {
				#do: "unknown"
				#provider: "test"
			}`,
			Error: cuex.ProviderFnNotFoundErr{Provider: "test", Fn: "unknown"},
		},
		"provider-fn-call-error": {
			Input: `x: {
				#do: "err"
				#provider: "test"
			}`,
			Error: cuex.FunctionCallError{Path: "x", Value: `x: {
				#do: "err"
				#provider: "test"
			}`, Err: fmt.Errorf("err")},
		},
		"provider-fn-timeout-error": {
			Input: `x: {
				#do: "timeout"
				#provider: "test"
			}`,
			Error: cuex.ResolveTimeoutErr{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			v := cctx.CompileString(tt.Input)
			_, err := compiler.Resolve(_ctx, v)
			require.Error(t, tt.Error, err)
		})
	}
}

type nestedStruct struct {
	Value string `json:"value"`
}

type testStruct struct {
	Val    string       `json:"val"`
	Nested nestedStruct `json:"nested"`
}

func TestWithExtraData(t *testing.T) {
	for name, tt := range map[string]any{
		"standard-map": map[string]interface{}{"key": map[string]interface{}{"k": "v"}},
		"raw-string":   "raw-string",
		"nil":          nil,
		"list":         []string{"a", "b"},
		"struct-data":  testStruct{Val: "a", Nested: nestedStruct{Value: "b"}},
		"struct-array": []testStruct{{Val: "a"}, {Val: "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			cuex.NewCompileConfig(cuex.WithExtraData(name, tt))
		})
	}
}

func TestInlineDoFunctionExecution(t *testing.T) {
	ctx := context.Background()

	t.Run("basic string", func(t *testing.T) {
		compiler := cuex.NewCompilerWithDefaultInternalPackages()
		code := strings.TrimSpace(`
			import "strings"

			parameter: {
				cluster: "cluster-1"
			}

			#Fn: {
				#do: {
					$returns: strings.ToUpper("\(parameter.cluster)-\($params.input)")
				}
				$params: {
					input: string
				}
				$returns: string
			}

			result: #Fn & {
              $params: {
                input: "test"
              }
            }
		`)
		val, err := compiler.CompileString(ctx, code)
		require.NoError(t, err)

		out := val.LookupPath(cue.ParsePath("result.$returns"))
		s, err := out.String()
		require.NoError(t, err)
		require.Equal(t, "CLUSTER-1-TEST", s)
	})

	t.Run("read from package", func(t *testing.T) {
		packagePath := "test/ext"
		packageObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "cue.oam.dev/v1alpha1",
				"kind":       "Package",
				"metadata": map[string]interface{}{
					"name":      "test-package",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"path": packagePath,
					"provider": map[string]interface{}{
						"endpoint": "",
						"protocol": "",
					},
					"templates": map[string]interface{}{
						"test/ext": strings.TrimSpace(`
							package ext

							import "strings"

							#Fn: {
								#do: {
									$returns: strings.ToUpper($params.input)
								}
								$params: {
									input: string
								}
								$returns: string
							}
						`),
					},
				},
			},
		}
		cl := fake.NewClientBuilder().Build()
		dcl := dynamicfake.NewSimpleDynamicClient(kuberuntime.NewScheme(), packageObj)
		singleton.KubeClient.Set(cl)
		singleton.DynamicClient.Set(dcl)

		compiler := cuex.NewCompilerWithDefaultInternalPackages()
		err := compiler.LoadExternalPackages(context.Background())
		assert.NoError(t, err)

		cuex.DefaultCompiler.Reload()
		defer singleton.ReloadClients()
		defer cuex.DefaultCompiler.Reload()

		tmpl := strings.TrimSpace(`
			import "test/ext"

			result: ext.#Fn & {
              $params: {
                input: "test"
              }
            }
		`)

		val, err := compiler.CompileString(ctx, tmpl)
		require.NoError(t, err)

		out := val.LookupPath(cue.ParsePath("result.$returns"))
		s, err := out.String()
		require.NoError(t, err)
		require.Equal(t, "TEST", s)
	})

	t.Run("read from package", func(t *testing.T) {
		packagePath := "test/ext"
		packageObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "cue.oam.dev/v1alpha1",
				"kind":       "Package",
				"metadata": map[string]interface{}{
					"name":      "test-package",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"path": packagePath,
					"provider": map[string]interface{}{
						"endpoint": "",
						"protocol": "",
					},
					"templates": map[string]interface{}{
						"test/ext": strings.TrimSpace(`
							package ext

							import "strings"

							#Fn: {
								in: string
								out: strings.ToUpper(in)
							}
						`),
					},
				},
			},
		}
		cl := fake.NewClientBuilder().Build()
		dcl := dynamicfake.NewSimpleDynamicClient(kuberuntime.NewScheme(), packageObj)
		singleton.KubeClient.Set(cl)
		singleton.DynamicClient.Set(dcl)

		compiler := cuex.NewCompilerWithDefaultInternalPackages()
		err := compiler.LoadExternalPackages(context.Background())
		assert.NoError(t, err)

		cuex.DefaultCompiler.Reload()
		defer singleton.ReloadClients()
		defer cuex.DefaultCompiler.Reload()

		tmpl := strings.TrimSpace(`
			import "test/ext"

			result: ext.#Fn & {
              in: "hello"
            }
		`)

		val, err := compiler.CompileString(ctx, tmpl)
		require.NoError(t, err)

		out := val.LookupPath(cue.ParsePath("result.out"))
		s, err := out.String()
		require.NoError(t, err)
		require.Equal(t, "HELLO", s)
	})

	t.Run("test type validation", func(t *testing.T) {
		packagePath := "test/types"
		packageObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "cue.oam.dev/v1alpha1",
				"kind":       "Package",
				"metadata": map[string]interface{}{
					"name":      "test-package",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"path": packagePath,
					"provider": map[string]interface{}{
						"endpoint": "",
						"protocol": "",
					},
					"templates": map[string]interface{}{
						"test/types": strings.TrimSpace(`
							package types

							#AWSRegion: =~"^[a-z]{2,3}-[a-z]+-\\d+$"
						`),
					},
				},
			},
		}
		cl := fake.NewClientBuilder().Build()
		dcl := dynamicfake.NewSimpleDynamicClient(kuberuntime.NewScheme(), packageObj)
		singleton.KubeClient.Set(cl)
		singleton.DynamicClient.Set(dcl)

		compiler := cuex.NewCompilerWithDefaultInternalPackages()
		err := compiler.LoadExternalPackages(context.Background())
		assert.NoError(t, err)

		cuex.DefaultCompiler.Reload()
		defer singleton.ReloadClients()
		defer cuex.DefaultCompiler.Reload()

		tmpl := strings.TrimSpace(`
			import "test/types"

			parameter: {
				region: types.#AWSRegion
			}

			parameter: {
				region: "us-east-1"
			}
				
			result: parameter.region
		`)

		val, err := compiler.CompileString(ctx, tmpl)
		require.NoError(t, err)

		out := val.LookupPath(cue.ParsePath("result"))
		s, err := out.String()
		require.NoError(t, err)
		require.Equal(t, "us-east-1", s)
	})
}
