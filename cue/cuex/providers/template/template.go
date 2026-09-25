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
	// Data is anything else the template should reach, under .data. It is
	// held raw so the numbers in it can be decoded exactly, which decoding
	// straight into map[string]any would not do.
	Data json.RawMessage `json:"data,omitempty"`
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

	own, err := decodeJSON(in.Params.Data)
	if err != nil {
		return nil, fmt.Errorf("data will not decode: %w", err)
	}
	data := map[string]any{"data": own}
	root, rooted := cuexruntime.RootFrom(ctx)
	for _, name := range ambient {
		if rooted {
			data[name] = decodeField(root, name)
		} else {
			data[name] = map[string]any{}
		}
	}

	out := &boundedWriter{ctx: ctx, left: maxRendered}
	if err = parsed.Execute(out, data); err != nil {
		return nil, fmt.Errorf("template will not render: %w", err)
	}
	return &RenderReturns{Returns: out.buf.String()}, nil
}

// maxRendered bounds what one render may produce. A template loops over what
// it is given, and what it is given comes from a user, so a run that would not
// stop has to be stopped. A config file is thousands of bytes, not millions.
const maxRendered = 1 << 20

// boundedWriter stops a render that will not stop itself, either because the
// time for it has gone or because it has written more than anything real
// would. Execute is not handed the context, so this is where both are seen.
type boundedWriter struct {
	ctx  context.Context
	buf  bytes.Buffer
	left int
}

func (in *boundedWriter) Write(p []byte) (int, error) {
	if err := in.ctx.Err(); err != nil {
		return 0, fmt.Errorf("gave up rendering: %w", err)
	}
	if len(p) > in.left {
		return 0, fmt.Errorf("rendered more than %d bytes, which is not a config file", maxRendered)
	}
	in.left -= len(p)
	return in.buf.Write(p)
}

// decodeField reads one of the ambient fields out of the value the call sits
// in. A field that is not there, or that cannot be read yet because a call it
// waits on has not run, is given to the template as nothing rather than
// failing the render: what the template does about it is the template's
// business, and missingkey will say so if it wanted the field.
func decodeField(root cue.Value, name string) map[string]any {
	field := root.LookupPath(cue.ParsePath(name))
	if !field.Exists() {
		return map[string]any{}
	}
	if whole, err := decodeValue(field); err == nil {
		return whole
	}
	// A block is marshalled whole where it can be, and field by field where it
	// cannot. One field a call has not filled in yet fails the whole marshal,
	// and reading that as "there is no parameter block" would hide every
	// sibling that was ready and blame whichever one the template asked for.
	out := map[string]any{}
	it, err := field.Fields()
	if err != nil {
		return out
	}
	for it.Next() {
		bs, err := it.Value().MarshalJSON()
		if err != nil {
			continue
		}
		var one any
		if err = decodeInto(bs, &one); err == nil {
			out[it.Label()] = one
		}
	}
	return out
}

func decodeValue(v cue.Value) (map[string]any, error) {
	bs, err := v.MarshalJSON()
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err = decodeInto(bs, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeJSON(raw json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := decodeInto(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// decodeInto reads JSON keeping whole numbers whole. Decoding into any makes
// every number a float64, and a template prints that with %v, so a memory
// limit of 1073741824 reaches a config file as 1.073741824e+09 and whatever
// reads it fails.
func decodeInto(bs []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(bs))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		return err
	}
	switch target := into.(type) {
	case *map[string]any:
		*target, _ = exactNumbers(*target).(map[string]any)
	case *any:
		*target = exactNumbers(*target)
	}
	return nil
}

// exactNumbers turns what UseNumber held back into the narrowest thing that
// prints and calculates the way the template author meant.
func exactNumbers(v any) any {
	switch node := v.(type) {
	case map[string]any:
		for k, each := range node {
			node[k] = exactNumbers(each)
		}
	case []any:
		for i, each := range node {
			node[i] = exactNumbers(each)
		}
	case json.Number:
		if i, err := node.Int64(); err == nil {
			return i
		}
		if f, err := node.Float64(); err == nil {
			return f
		}
		return node.String()
	}
	return v
}

// alsoNotRepeatable are the helpers slim-sprig's own hermetic set leaves in
// but a definition still must not reach.
//
// A rendered manifest is compared against the last one to see what drifted, so
// a helper that answers differently twice stops an application settling. ago
// reads the clock, randInt is random, and durationRound reaches for the
// current time when handed one. toDate reads in whatever zone is running it,
// and the os path helpers follow whatever separator is, so a definition would
// render one way in the controller and another in the CLI.
var alsoNotRepeatable = []string{
	"ago",
	"randInt",
	"durationRound",
	"toDate", "mustToDate",
	"osBase", "osClean", "osDir", "osExt", "osIsAbs",
}

// funcs is built once: sprig copies its whole map on every call and
// text/template copies it again, which is not worth paying per render.
//
// The base is slim-sprig's hermetic set rather than a list kept here. It names
// what is not repeatable and goes on naming it as the library grows, and it
// already covers the whole date family - where date, handed the string a CUE
// value gives it, quietly answers with the current time instead.
var funcs = buildFuncs()

func buildFuncs() template.FuncMap {
	built := sprig.HermeticTxtFuncMap()
	for _, name := range alsoNotRepeatable {
		delete(built, name)
	}
	return built
}

// Funcs is what a template may call, exported so a test can hold it to a known
// list: a dependency bump that changes one should be looked at rather than
// picked up in silence.
func Funcs() template.FuncMap {
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
