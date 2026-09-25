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
	"testing"

	"github.com/kubevela/pkg/cue/cuex"
)

// the same sixty line config file built each way. CUE does it with a
// comprehension and a join, which is the idiomatic way and costs no provider
// call at all, because there is no call.
const inCUE = `
import (
	"list"
	"strings"
)
parameter: {port: 8080, host: "backend"}
lines: [for i in list.Range(0, %d, 1) {
	"  location /p\(i) { proxy_pass http://\(parameter.host):\(parameter.port)/\(i); }"
}]
out: "server {\n" + strings.Join(lines, "\n") + "\n}"
`

const inTemplate = `
import "vela/template"
parameter: {port: 8080, host: "backend", n: %d}
render: template.#Render & {$params: template: """
	server {
	{{- range until (int .parameter.n) }}
	  location /p{{ . }} { proxy_pass http://{{ $.parameter.host }}:{{ $.parameter.port }}/{{ . }}; }
	{{- end }}
	}
	"""}
out: render.$returns
`

// BenchmarkTemplateVsCUE is the question an author actually has: is reaching
// for a template cheaper than writing the same text in CUE?
func BenchmarkTemplateVsCUE(b *testing.B) {
	c := cuex.NewCompilerWithDefaultInternalPackages()
	ctx := context.Background()
	for _, n := range []int{5, 10, 20, 40, 60, 120} {
		for name, src := range map[string]string{
			"cue":      fmt.Sprintf(inCUE, n),
			"template": fmt.Sprintf(inTemplate, n),
		} {
			b.Run(fmt.Sprintf("lines=%d/%s", n, name), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := c.CompileString(ctx, src); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
