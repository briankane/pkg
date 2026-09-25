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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kubevela/pkg/cue/cuex/providers"
	"github.com/kubevela/pkg/cue/cuex/providers/template"
)

// TestRenderStopsWhenTheTimeHasGone: Resolve looks at the deadline between
// calls, so a render already out of time has to notice by itself rather than
// run to the end of a loop first.
func TestRenderStopsWhenTheTimeHasGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := template.Render(ctx, &providers.Params[template.RenderParams]{
		Params: template.RenderParams{Template: `{{ range until 100 }}x{{ end }}`},
	})
	require.ErrorContains(t, err, "gave up rendering")
	require.ErrorIs(t, err, context.Canceled)
}

// TestRenderRejectsDataThatIsNotAnObject: $params.data is read raw so its
// numbers survive, which means what it holds is no longer checked by the
// decode into the input type, and has to be checked here.
func TestRenderRejectsDataThatIsNotAnObject(t *testing.T) {
	_, err := template.Render(context.Background(), &providers.Params[template.RenderParams]{
		Params: template.RenderParams{
			Template: `{{ .data }}`,
			Data:     json.RawMessage(`["not", "an", "object"]`),
		},
	})
	require.ErrorContains(t, err, "data will not decode")
}

// TestRenderKeepsNumbersItCannotNarrow: a whole number wider than an int64
// still has to print as something a config file can hold, rather than being
// dropped for not fitting.
func TestRenderKeepsNumbersItCannotNarrow(t *testing.T) {
	for name, tt := range map[string]struct {
		data string
		want string
	}{
		"fits in an int64":    {`{"n": 9007199254740993}`, "9007199254740993"},
		"wider than an int64": {`{"n": 123456789012345678901234567890}`, "1.2345678901234568e+29"},
		"has a fraction":      {`{"n": 1.5}`, "1.5"},
		"negative":            {`{"n": -1073741824}`, "-1073741824"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := template.Render(context.Background(), &providers.Params[template.RenderParams]{
				Params: template.RenderParams{Template: `{{ .data.n }}`, Data: json.RawMessage(tt.data)},
			})
			require.NoError(t, err)
			require.Equal(t, tt.want, out.Returns)
		})
	}
}

// TestRenderReachesNestedNumbers: the narrowing has to reach through the
// lists and maps a parameter block is made of, not only its top level.
func TestRenderReachesNestedNumbers(t *testing.T) {
	out, err := template.Render(context.Background(), &providers.Params[template.RenderParams]{
		Params: template.RenderParams{
			Template: `{{ (index .data.ports 0).port }}/{{ .data.limits.memory }}`,
			Data:     json.RawMessage(`{"ports": [{"port": 1073741824}], "limits": {"memory": 2147483648}}`),
		},
	})
	require.NoError(t, err)
	require.Equal(t, "1073741824/2147483648", out.Returns)
}
