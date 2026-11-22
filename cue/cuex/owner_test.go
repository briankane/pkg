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

func TestOwner(t *testing.T) {
	t.Run("a context with none has none", func(t *testing.T) {
		_, ok := cuex.OwnerFrom(context.Background())
		require.False(t, ok)
	})
	t.Run("the owner set is the owner read", func(t *testing.T) {
		owner := cuex.Owner{Kind: "WorkflowStepDefinition", Namespace: "team-a", Name: "close-stale",
			Writer: &cuex.Writer{User: "alice", Groups: []string{"payments-leads"}}}
		got, ok := cuex.OwnerFrom(cuex.WithOwner(context.Background(), owner))
		require.True(t, ok)
		require.Equal(t, owner, got)
	})
	t.Run("an inner owner replaces the outer, as each level of an extends chain does", func(t *testing.T) {
		ctx := cuex.WithOwner(context.Background(), cuex.Owner{Kind: "ComponentDefinition", Namespace: "vela-system", Name: "base"})
		ctx = cuex.WithOwner(ctx, cuex.Owner{Kind: "ComponentDefinition", Namespace: "team-a", Name: "child"})
		got, _ := cuex.OwnerFrom(ctx)
		require.Equal(t, "child", got.Name)
	})
}
