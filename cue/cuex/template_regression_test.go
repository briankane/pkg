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

	"github.com/stretchr/testify/require"

	"github.com/kubevela/pkg/cue/cuex"
)

// TestTemplateKeepsWholeNumbersWhole: JSON makes every number a float64, and a
// template prints that with %v, so a memory limit of 1073741824 reaches a
// config file as 1.073741824e+09 and whatever reads it fails. Anything under a
// million prints normally, which is how this hides.
func TestTemplateKeepsWholeNumbersWhole(t *testing.T) {
	v, err := cuex.NewCompilerWithDefaultInternalPackages().CompileString(context.Background(), `
import "vela/template"
parameter: {mem: 1073741824, port: 8080, ratio: 1.5}
render: template.#Render & {$params: {
	template: "mem={{ .parameter.mem }} port={{ .parameter.port }} ratio={{ .parameter.ratio }} d={{ .data.limit }}"
	data: limit: 5000000
}}
out: render.$returns
`)
	require.NoError(t, err)
	require.Equal(t, "mem=1073741824 port=8080 ratio=1.5 d=5000000", rendered(t, v, "out"))
}

// TestTemplateReadsWhatIsReady: a block is marshalled whole where it can be
// and field by field where it cannot. One field the schema leaves open must
// not take its siblings with it, and the error has to name the field the
// template actually asked for rather than the first one it reached.
func TestTemplateReadsWhatIsReady(t *testing.T) {
	c := cuex.NewCompilerWithDefaultInternalPackages()
	v, err := c.CompileString(context.Background(), `
import "vela/template"
parameter: {name: "app", notYet: string}
render: template.#Render & {$params: template: "{{ .parameter.name }}"}
out: render.$returns
`)
	require.NoError(t, err, "a sibling that is not concrete must not hide one that is")
	require.Equal(t, "app", rendered(t, v, "out"))

	_, err = c.CompileString(context.Background(), `
import "vela/template"
parameter: {name: "app", notYet: string}
render: template.#Render & {$params: template: "{{ .parameter.notYet }}"}
out: render.$returns
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "notYet", "the error should name the field that was not ready")
}

// TestTemplateStopsARunawayRender: a template loops over what it is given, and
// what it is given comes from a user. Execute is not handed the context, so a
// render that will not stop has to be stopped by what it writes into.
func TestTemplateStopsARunawayRender(t *testing.T) {
	_, err := cuex.NewCompilerWithDefaultInternalPackages().CompileString(context.Background(), `
import "vela/template"
parameter: {}
render: template.#Render & {$params: template: "{{ range until 100000000 }}xxxxxxxxxx{{ end }}"}
out: render.$returns
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a config file")
}

// TestTemplateDateCannotReachTheClock is the one that looked safe. sprig's
// date takes a format and a time, and anything that is not a time falls
// through to the current one, so date on a parameter answered with today
// rather than with what it was handed.
func TestTemplateDateCannotReachTheClock(t *testing.T) {
	_, err := cuex.NewCompilerWithDefaultInternalPackages().CompileString(context.Background(), `
import "vela/template"
parameter: when: "2020-01-01"
render: template.#Render & {$params: template: "{{ date \"2006-01-02\" .parameter.when }}"}
out: render.$returns
`)
	require.Error(t, err, "date has to be out of reach, not quietly answering with today")
	require.Contains(t, err.Error(), "not defined")
}
