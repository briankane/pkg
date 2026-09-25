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

// Package template renders Go text/template from a CUE template, for the text
// a manifest has to carry but CUE is poor at building: an nginx.conf in a
// ConfigMap, a properties file, a shell script.
//
// It renders text and nothing else. What comes back is a string, and putting
// it somewhere is the calling template's business, so CUE keeps hold of the
// structure, the schema and the patching.
package template

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"text/template"

	_ "embed"

	"cuelang.org/go/cue"
	sprig "github.com/go-task/slim-sprig/v3"

	"github.com/kubevela/pkg/cue/cuex/providers"
	cuexruntime "github.com/kubevela/pkg/cue/cuex/runtime"
	"github.com/kubevela/pkg/util/runtime"
)

// RenderParams is what a call passes in.
type RenderParams struct {
	// Template is the Go text/template to render.
	Template string `json:"template"`
	// Data is anything else the template should reach, under .data
	Data map[string]any `json:"data,omitempty"`
}

// RenderVars .
type RenderVars = providers.Params[RenderParams]

// RenderReturns .
type RenderReturns = providers.Returns[string]

// ambient names what a template is given besides its own data. They come from
// the value the call sits in rather than from the call, so a template reaches
// them without the calling CUE having to pass them down.
var ambient = []string{"context", "parameter"}

// Render renders the template against the context and parameters of the value
// the call sits in, plus whatever else the call passed as data.
//
//	{{ .context.name }}       what the application is called
//	{{ .parameter.image }}    what the component was given
//	{{ .data.anything }}      what this call passed in
//
// A name the data does not hold is an error rather than an empty string. CUE
// would not let a template reference a field that is not there either, and a
// config file with a silent gap in it is worse than one that failed to render.
func Render(ctx context.Context, in *RenderVars) (*RenderReturns, error) {
	if in.Params.Template == "" {
		return nil, fmt.Errorf("template is empty, so there is nothing to render")
	}
	parsed, err := template.New("render").
		Funcs(Funcs()).
		Option("missingkey=error").
		Parse(in.Params.Template)
	if err != nil {
		return nil, fmt.Errorf("template will not parse: %w", err)
	}

	data := map[string]any{"data": orEmpty(in.Params.Data)}
	if root, ok := cuexruntime.RootFrom(ctx); ok {
		for _, name := range ambient {
			data[name] = orEmpty(decodeField(root, name))
		}
	} else {
		for _, name := range ambient {
			data[name] = map[string]any{}
		}
	}

	var out bytes.Buffer
	if err = parsed.Execute(&out, data); err != nil {
		return nil, fmt.Errorf("template will not render: %w", err)
	}
	return &RenderReturns{Returns: out.String()}, nil
}

// decodeField reads one of the ambient fields out of the value the call sits
// in. A field that is not there, or that cannot be read yet because a call it
// waits on has not run, is given to the template as nothing rather than
// failing the render: what the template does about it is the template's
// business, and missingkey will say so if it wanted the field.
func decodeField(root cue.Value, name string) map[string]any {
	field := root.LookupPath(cue.ParsePath(name))
	if !field.Exists() {
		return nil
	}
	bs, err := field.MarshalJSON()
	if err != nil {
		return nil
	}
	out := map[string]any{}
	if err = json.Unmarshal(bs, &out); err != nil {
		return nil
	}
	return out
}

func orEmpty(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	return in
}

// nonDeterministic are the helpers a definition must not reach.
//
// A rendered manifest is compared against the last one to find what drifted,
// so a template that reads the clock, the environment or a random number
// renders differently every time and the application never settles. Reading
// the controller's own environment would also put whatever is in it into a
// user's manifest. The os path helpers are here because they follow the
// separator of whatever is running them, so a definition would render one way
// in the controller and another in the CLI.
var nonDeterministic = []string{
	"now", "ago",
	"env", "expandenv",
	"getHostByName",
	"randInt",
	"osBase", "osClean", "osDir", "osExt", "osIsAbs",
}

// Funcs is what a template may call. It is slim-sprig less the helpers above,
// and is exported so a test can hold it to a known list: a dependency bump
// that adds a helper should be looked at rather than picked up in silence.
func Funcs() template.FuncMap {
	funcs := sprig.TxtFuncMap()
	for _, name := range nonDeterministic {
		delete(funcs, name)
	}
	return funcs
}

// ProviderName .
const ProviderName = "template"

//go:embed template.cue
var cueTemplate string

// Package .
var Package = runtime.Must(cuexruntime.NewInternalPackage(ProviderName, cueTemplate, map[string]cuexruntime.ProviderFn{
	"render": cuexruntime.GenericProviderFn[RenderVars, RenderReturns](Render),
}))
