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
	"testing"

	"cuelang.org/go/cue"
	"github.com/stretchr/testify/require"

	"github.com/kubevela/pkg/cue/cuex"
)

func rendered(t *testing.T, v cue.Value, path string) string {
	t.Helper()
	s, err := v.LookupPath(cue.ParsePath(path)).String()
	require.NoError(t, err)
	return s
}

// TestTemplateProviderReachesTheSurroundingValue is the whole point of the
// provider: the calling CUE says nothing about context or parameter, and the
// template reads both anyway.
func TestTemplateProviderReachesTheSurroundingValue(t *testing.T) {
	v, err := cuex.NewCompilerWithDefaultInternalPackages().CompileString(context.Background(), `
import "vela/template"

context: {name: "my-app", namespace: "prod"}
parameter: {port: 8080, upstream: "backend"}

render: template.#Render & {$params: template: """
	server {
	  listen {{ .parameter.port }};
	  server_name {{ .context.name }}.{{ .context.namespace }};
	  location / { proxy_pass http://{{ .parameter.upstream }}; }
	}
	"""}
conf: render.$returns
`)
	require.NoError(t, err)
	require.Equal(t, `server {
  listen 8080;
  server_name my-app.prod;
  location / { proxy_pass http://backend; }
}`, rendered(t, v, "conf"))
}

// TestTemplateProviderTakesAnotherCallsResult: the resolver runs the calls in
// the order the references put them in, so a template can be handed what an
// earlier call produced.
func TestTemplateProviderTakesAnotherCallsResult(t *testing.T) {
	v, err := cuex.NewCompilerWithDefaultInternalPackages().CompileString(context.Background(), `
import (
	"vela/base64"
	"vela/template"
)

parameter: secret: "hunter2"

encoded: base64.#Encode & {$params: parameter.secret}

render: template.#Render & {$params: {
	template: "TOKEN={{ .data.token }}"
	data: token: encoded.$returns
}}
env: render.$returns
`)
	require.NoError(t, err)
	require.Equal(t, "TOKEN=aHVudGVyMg==", rendered(t, v, "env"))
}

// TestTemplateProviderRendersTheSameTwice: two compiles of one template have
// to agree, or a drift check never settles.
func TestTemplateProviderRendersTheSameTwice(t *testing.T) {
	c := cuex.NewCompilerWithDefaultInternalPackages()
	src := `
import "vela/template"
context: name: "app"
parameter: items: ["a", "b", "c"]
render: template.#Render & {$params: template: "{{ range .parameter.items }}{{ . | upper }};{{ end }}{{ .context.name }}"}
out: render.$returns
`
	first, err := c.CompileString(context.Background(), src)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		again, err := c.CompileString(context.Background(), src)
		require.NoError(t, err)
		require.Equal(t, rendered(t, first, "out"), rendered(t, again, "out"), "compile %d differed", i)
	}
}

// TestTemplateProviderReportsAFailure: a template that cannot render has to
// fail the compile rather than leave a hole in a manifest.
func TestTemplateProviderReportsAFailure(t *testing.T) {
	_, err := cuex.NewCompilerWithDefaultInternalPackages().CompileString(context.Background(), `
import "vela/template"
parameter: {}
render: template.#Render & {$params: template: "{{ .parameter.missing }}"}
out: render.$returns
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "will not render")
}
