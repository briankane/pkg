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

package runtime

import (
	"context"

	"cuelang.org/go/cue"
)

type rootKey struct{}

// WithRoot carries the value a resolve is working on.
//
// A provider function is handed its own call and nothing else, and cue.Value
// offers no way back up, so a function needing what surrounds it - the
// template's context or its parameters - has no way to ask. The resolver has
// the whole value in hand while it runs the calls, so it puts it here.
//
// The value moves on as each call is filled in, so what a function reads is
// the value as it stood when the function was reached, not as it started.
func WithRoot(ctx context.Context, root cue.Value) context.Context {
	return context.WithValue(ctx, rootKey{}, root)
}

// RootFrom returns the value the resolve is working on, and whether there was
// one. There is not, for a provider function called directly rather than
// through a resolve, so a caller has to cope with its absence.
func RootFrom(ctx context.Context) (cue.Value, bool) {
	root, ok := ctx.Value(rootKey{}).(cue.Value)
	return root, ok
}
