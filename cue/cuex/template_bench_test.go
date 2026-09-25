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
	"fmt"
	"strings"
	"testing"

	"github.com/kubevela/pkg/cue/cuex"
)

// nCallTemplate is n provider calls and nothing else, for measuring what the
// resolver spends per call rather than what a provider does.
func nCallTemplate(n int) string {
	var b strings.Builder
	b.WriteString("import \"vela/base64\"\ncalls: {\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\t\"%d\": base64.#Encode & {$params: \"k-%d\"}\n", i, i)
	}
	b.WriteString("}\n")
	return b.String()
}

// BenchmarkResolveWithRoot measures the resolver's per-call cost, which now
// carries the value each call sits in. Every provider pays that, not only the
// ones that read it.
func BenchmarkResolveWithRoot(b *testing.B) {
	c := cuex.NewCompilerWithDefaultInternalPackages()
	ctx := context.Background()
	for _, n := range []int{1, 10, 50} {
		src := nCallTemplate(n)
		b.Run(fmt.Sprintf("calls=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := c.CompileString(ctx, src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkTemplateRender is what the provider itself costs, against a
// base64 call of the same shape as a floor for a call that does nothing much.
func BenchmarkTemplateRender(b *testing.B) {
	c := cuex.NewCompilerWithDefaultInternalPackages()
	ctx := context.Background()

	small := `
import "vela/template"
context: name: "app"
parameter: {port: 8080, host: "backend"}
render: template.#Render & {$params: template: "listen {{ .parameter.port }}; upstream {{ .parameter.host }}; # {{ .context.name }}"}
out: render.$returns
`
	var body strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&body, "  location /p%d { proxy_pass http://{{ .parameter.host }}:{{ .parameter.port }}/%d; }\\n", i, i)
	}
	large := fmt.Sprintf(`
import "vela/template"
context: name: "app"
parameter: {port: 8080, host: "backend"}
render: template.#Render & {$params: template: "server {\n%s}"}
out: render.$returns
`, body.String())

	floor := `
import "vela/base64"
parameter: secret: "hunter2"
enc: base64.#Encode & {$params: parameter.secret}
out: enc.$returns
`
	for name, src := range map[string]string{
		"base64 floor":    floor,
		"template small":  small,
		"template 60line": large,
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := c.CompileString(ctx, src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
