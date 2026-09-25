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

// webserviceBody is the shape a component definition actually is: a parameter
// block of some size and a workload built from it. The benchmarks above used
// five lines of CUE, which hides how much of a compile is the build rather
// than the call.
const webserviceBody = `
parameter: {
	image:            string
	imagePullPolicy?: *"IfNotPresent" | "Always" | "Never"
	imagePullSecrets?: [...string]
	ports?: [...{
		port:      int
		protocol:  *"TCP" | "UDP"
		expose:    *false | bool
		nodePort?: int
	}]
	cmd?: [...string]
	env?: [...{
		name:   string
		value?: string
		valueFrom?: {
			secretKeyRef?: {name: string, key: string}
			configMapKeyRef?: {name: string, key: string}
		}
	}]
	cpu?:      string
	memory?:   string
	replicas?: *1 | int
	labels?: [string]:      string
	annotations?: [string]: string
	livenessProbe?: {
		httpGet?: {path: string, port: int}
		initialDelaySeconds?: *0 | int
		periodSeconds?:       *10 | int
	}
	readinessProbe?: {
		httpGet?: {path: string, port: int}
		initialDelaySeconds?: *0 | int
		periodSeconds?:       *10 | int
	}
	volumeMounts?: [...{name: string, mountPath: string, readOnly?: *false | bool}]
	hostAliases?: [...{ip: string, hostnames: [...string]}]
}

output: {
	apiVersion: "apps/v1"
	kind:       "Deployment"
	metadata: {
		name:      context.name
		namespace: context.namespace
		if parameter.labels != _|_ {labels: parameter.labels}
		if parameter.annotations != _|_ {annotations: parameter.annotations}
	}
	spec: {
		replicas: parameter.replicas
		selector: matchLabels: "app.oam.dev/component": context.name
		template: {
			metadata: labels: "app.oam.dev/component": context.name
			spec: {
				containers: [{
					name:  context.name
					image: parameter.image
					if parameter.imagePullPolicy != _|_ {imagePullPolicy: parameter.imagePullPolicy}
					if parameter.cmd != _|_ {command: parameter.cmd}
					if parameter.env != _|_ {env: parameter.env}
					if parameter.ports != _|_ {
						ports: [for p in parameter.ports {containerPort: p.port, protocol: p.protocol}]
					}
					if parameter.cpu != _|_ {
						resources: requests: cpu: parameter.cpu
					}
					if parameter.memory != _|_ {
						resources: requests: memory: parameter.memory
					}
					if parameter.livenessProbe != _|_ {livenessProbe: parameter.livenessProbe}
					if parameter.readinessProbe != _|_ {readinessProbe: parameter.readinessProbe}
					if parameter.volumeMounts != _|_ {volumeMounts: parameter.volumeMounts}
				}]
				if parameter.imagePullSecrets != _|_ {
					imagePullSecrets: [for s in parameter.imagePullSecrets {name: s}]
				}
				if parameter.hostAliases != _|_ {hostAliases: parameter.hostAliases}
			}
		}
	}
}
`

const realisticContext = `
context: {name: "my-app", namespace: "prod", appName: "my-app"}
parameter: {
	image:    "nginx:1.25"
	replicas: 3
	cpu:      "500m"
	ports: [{port: 8080, protocol: "TCP", expose: true}]
}
`

// BenchmarkRealisticDefinition puts the template call inside a definition of
// the size one actually is, so the render is measured against the build it
// sits in rather than on its own.
func BenchmarkRealisticDefinition(b *testing.B) {
	c := cuex.NewCompilerWithDefaultInternalPackages()
	ctx := context.Background()

	var conf strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&conf, "  location /p%d { proxy_pass http://{{ $.context.name }}:{{ (index $.parameter.ports 0).port }}/%d; }\n", i, i)
	}

	plain := realisticContext + webserviceBody
	// the import has to lead the file, so it is prepended rather than appended
	withTemplate := "import \"vela/template\"\n" + plain + fmt.Sprintf(`
render: template.#Render & {$params: template: %q}
outputs: conf: {
	apiVersion: "v1"
	kind:       "ConfigMap"
	metadata: name: context.name + "-conf"
	data: "nginx.conf": render.$returns
}
`, "server {\n"+conf.String()+"}")

	// a call that does almost nothing, to tell the render apart from what the
	// resolver spends finding and filling any call at all
	withBase64 := "import \"vela/base64\"\n" + plain + `
enc: base64.#Encode & {$params: parameter.image}
outputs: conf: {
	apiVersion: "v1"
	kind:       "ConfigMap"
	metadata: name: context.name + "-conf"
	data: "image.b64": enc.$returns
}
`

	for name, src := range map[string]string{
		"definition only":      plain,
		"definition +base64":   withBase64,
		"definition +template": withTemplate,
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
