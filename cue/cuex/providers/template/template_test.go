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

package template_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/stretchr/testify/require"

	"github.com/kubevela/pkg/cue/cuex/providers"
	"github.com/kubevela/pkg/cue/cuex/providers/template"
	cuexruntime "github.com/kubevela/pkg/cue/cuex/runtime"
)

func render(t *testing.T, ctx context.Context, tmpl string, data map[string]any) (string, error) {
	t.Helper()
	out, err := template.Render(ctx, &providers.Params[template.RenderParams]{
		Params: template.RenderParams{Template: tmpl, Data: data},
	})
	if err != nil {
		return "", err
	}
	return out.Returns, nil
}

// rootedCtx is what the resolver gives a provider: the value the call sits in.
func rootedCtx(t *testing.T, src string) context.Context {
	t.Helper()
	root := cuecontext.New().CompileString(src)
	require.NoError(t, root.Err())
	return cuexruntime.WithRoot(context.Background(), root)
}

// TestRenderReachesContextAndParameter is the point of carrying the root: a
// template reads what surrounds its call without the calling CUE passing it.
func TestRenderReachesContextAndParameter(t *testing.T) {
	ctx := rootedCtx(t, `
context: {name: "my-app", namespace: "prod"}
parameter: {image: "nginx:1.25", replicas: 3}`)

	got, err := render(t, ctx,
		`{{ .context.namespace }}/{{ .context.name }} runs {{ .parameter.replicas }}x {{ .parameter.image }}`, nil)
	require.NoError(t, err)
	require.Equal(t, "prod/my-app runs 3x nginx:1.25", got)
}

// TestRenderReachesItsOwnData covers what the call passed, which is how a
// result the resolver produced earlier gets into the text.
func TestRenderReachesItsOwnData(t *testing.T) {
	ctx := rootedCtx(t, `context: {name: "a"}`)
	got, err := render(t, ctx, `upstream {{ .data.host }}:{{ .data.port }};`,
		map[string]any{"host": "svc.prod", "port": 8080})
	require.NoError(t, err)
	require.Equal(t, "upstream svc.prod:8080;", got)
}

// TestRenderWithoutARoot: a function called outside a resolve has no value
// around it, and asking for one must not panic.
func TestRenderWithoutARoot(t *testing.T) {
	got, err := render(t, context.Background(), `{{ .data.k }}`, map[string]any{"k": "v"})
	require.NoError(t, err)
	require.Equal(t, "v", got)

	_, err = render(t, context.Background(), `{{ .context.name }}`, nil)
	require.Error(t, err, "there is no context to read, and saying so beats an empty string")
}

// TestRenderFailsOnAMissingName: a config file with a silent gap in it is
// worse than one that did not render.
func TestRenderFailsOnAMissingName(t *testing.T) {
	ctx := rootedCtx(t, `parameter: {image: "nginx"}`)
	_, err := render(t, ctx, `{{ .parameter.notThere }}`, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "will not render")
}

func TestRenderRejectsWhatItCannotUse(t *testing.T) {
	ctx := context.Background()
	_, err := render(t, ctx, "", nil)
	require.ErrorContains(t, err, "nothing to render")

	_, err = render(t, ctx, `{{ .data.unclosed `, nil)
	require.ErrorContains(t, err, "will not parse")
}

// TestRenderIsDeterministic is the property the whole helper list is chosen
// for: the same call twice has to give the same text, or a drift check never
// settles.
func TestRenderIsDeterministic(t *testing.T) {
	ctx := rootedCtx(t, `context: {name: "a"}, parameter: {n: 3}`)
	tmpl := `{{ range until (int .parameter.n) }}{{ . }},{{ end }}{{ .context.name | upper }}`
	first, err := render(t, ctx, tmpl, nil)
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		again, err := render(t, ctx, tmpl, nil)
		require.NoError(t, err)
		require.Equal(t, first, again, "run %d differed", i)
	}
}

// TestNonDeterministicHelpersAreGone: each of these would make a definition
// render differently every reconcile, and env would put the controller's own
// environment into a user's manifest.
func TestNonDeterministicHelpersAreGone(t *testing.T) {
	funcs := template.Funcs()
	for _, name := range []string{
		"now", "ago", "env", "expandenv", "getHostByName", "randInt",
		"osBase", "osClean", "osDir", "osExt", "osIsAbs",
	} {
		_, found := funcs[name]
		require.False(t, found, "%q has to stay out of reach", name)
	}
	for _, name := range []string{"upper", "trim", "b64enc", "toJson", "indent", "default"} {
		_, found := funcs[name]
		require.True(t, found, "%q is the reason for having helpers at all", name)
	}
}

// TestTheHelperListIsPinned holds the set to what was reviewed. A dependency
// bump that adds a helper fails here, which is the point: the new one has to
// be looked at before a definition can reach it.
func TestTheHelperListIsPinned(t *testing.T) {
	names := make([]string, 0, len(template.Funcs()))
	for name := range template.Funcs() {
		names = append(names, name)
	}
	sort.Strings(names)
	require.Equal(t, 155, len(names),
		"the helper list changed, review the additions and update this count:\n%s",
		strings.Join(names, " "))
}
