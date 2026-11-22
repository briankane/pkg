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

package cuex

import (
	"context"
	"fmt"

	"cuelang.org/go/cue"

	cuexruntime "github.com/kubevela/pkg/cue/cuex/runtime"
)

// CallCheck decides whether a call may run. It sees the compilation's context,
// owner included, the provider the call goes to, its #do, and the call's value,
// whose conjuncts say which files wrote it.
type CallCheck func(ctx context.Context, provider cuexruntime.Provider, fn string, call cue.Value) error

// gate is what a compilation's calls are checked against: the compiler's check,
// and the providers the calls name.
type gate struct {
	check     CallCheck
	providers map[string]cuexruntime.Provider
}

type gateKey struct{}

// gated returns ctx carrying the gate for calls resolved against providers. The
// concurrent path decodes each call with nothing but this context, so the gate
// travels in it rather than around a provider.
func (in *Compiler) gated(ctx context.Context, providers map[string]cuexruntime.Provider) context.Context {
	return context.WithValue(ctx, gateKey{}, gate{check: in.CallCheck, providers: providers})
}

// checkCall decides whether call may run.
func checkCall(ctx context.Context, call pendingCall) error {
	g, ok := ctx.Value(gateKey{}).(gate)
	if !ok {
		return nil
	}
	provider := g.providers[call.provider]
	if g.check != nil {
		return g.check(ctx, provider, call.fn, call.value)
	}
	if p, ok := provider.(cuexruntime.FromPackage); ok && p.Source().Spec.Restrictions != nil {
		src := p.Source()
		return fmt.Errorf("%s/%s is restricted, and nothing here checks who may call it", src.Namespace, src.Name)
	}
	return nil
}
