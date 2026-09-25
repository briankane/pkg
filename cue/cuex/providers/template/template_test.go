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
	"encoding/json"
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
	// $params.data arrives as it came off the wire, so a test hands it over
	// the same way rather than as a decoded map
	var raw json.RawMessage
	if data != nil {
		bs, err := json.Marshal(data)
		require.NoError(t, err)
		raw = bs
	}
	out, err := template.Render(ctx, &providers.Params[template.RenderParams]{
		Params: template.RenderParams{Template: tmpl, Data: raw},
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
//
// The date family is the one worth naming. sprig's date takes a format and a
// time, but anything that is not a time or an int falls through to the current
// time, and what comes out of a CUE value is a string. So date on a parameter
// silently answers with today rather than with what it was given.
func TestNonDeterministicHelpersAreGone(t *testing.T) {
	funcs := template.Funcs()
	for _, name := range []string{
		"now", "ago",
		"date", "dateInZone", "date_in_zone", "dateModify", "date_modify",
		"htmlDate", "htmlDateInZone", "toDate", "mustToDate", "durationRound",
		"env", "expandenv", "getHostByName",
		"randInt", "randAlpha", "randAlphaNum", "randAscii", "randNumeric", "randBytes", "uuidv4",
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

// helpers is what a template may call, as reviewed. A count alone would pass a
// bump that took one away and gave another back, which is the very change that
// would put a clock-reading helper within reach under a new name.
var helpers = []string{
	"add", "add1", "adler32sum", "all", "any", "append", "atoi", "b32dec",
	"b32enc", "b64dec", "b64enc", "base", "biggest", "cat", "ceil", "chunk",
	"clean", "coalesce", "compact", "concat", "contains", "deepEqual", "default", "dict",
	"dig", "dir", "div", "duration", "empty", "ext", "fail", "first",
	"float64", "floor", "fromJson", "get", "has", "hasKey", "hasPrefix", "hasSuffix",
	"hello", "indent", "initial", "int", "int64", "isAbs", "join", "keys",
	"kindIs", "kindOf", "last", "list", "lower", "max", "maxf", "min",
	"minf", "mod", "mul", "mustAppend", "mustChunk", "mustCompact", "mustDateModify", "mustFirst",
	"mustFromJson", "mustHas", "mustInitial", "mustLast", "mustPrepend", "mustPush", "mustRegexFind", "mustRegexFindAll", "mustRegexMatch",
	"mustRegexReplaceAll", "mustRegexReplaceAllLiteral", "mustRegexSplit", "mustRest", "mustReverse", "mustSlice", "mustToJson", "mustToPrettyJson",
	"mustToRawJson", "mustUniq", "mustWithout", "must_date_modify", "nindent", "omit", "pick", "pluck",
	"plural", "prepend", "push", "quote", "regexFind", "regexFindAll", "regexMatch", "regexQuoteMeta", "regexReplaceAll",
	"regexReplaceAllLiteral", "regexSplit", "repeat", "replace", "rest", "reverse", "round", "seq",
	"set", "sha1sum", "sha256sum", "slice", "sortAlpha", "split", "splitList", "splitn",
	"squote", "sub", "substr", "ternary", "title", "toDecimal", "toJson", "toPrettyJson",
	"toRawJson", "toString", "toStrings", "trim", "trimAll", "trimPrefix", "trimSuffix", "trimall",
	"trunc", "tuple", "typeIs", "typeIsLike", "typeOf", "uniq", "unixEpoch", "unset",
	"until", "untilStep", "upper", "urlJoin", "urlParse", "values", "without",
}

// TestTheHelperListIsPinned holds the set to what was reviewed. A dependency
// bump that changes one fails here, which is the point: it has to be looked at
// before a definition can reach it.
func TestTheHelperListIsPinned(t *testing.T) {
	names := make([]string, 0, len(template.Funcs()))
	for name := range template.Funcs() {
		names = append(names, name)
	}
	sort.Strings(names)
	require.Equal(t, helpers, names,
		"the helper list changed, review it and update the list above:\n%s",
		strings.Join(names, " "))
}
