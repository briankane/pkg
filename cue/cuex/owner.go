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

import "context"

// Owner is the definition a compilation is for. It travels in the context every
// compile takes, set where definitions are loaded, since most call sites hold only
// a CUE string.
type Owner struct {
	// Kind is the definition's kind, such as ComponentDefinition.
	Kind      string
	Namespace string
	Name      string
	// Writer is who last changed the definition's spec, from its writer stamp. Nil
	// means none is known, and the writer holds no role or grant.
	Writer *Writer
}

// Writer is a user and the groups it carried when it wrote an object.
type Writer struct {
	User   string
	Groups []string
}

type ownerKey struct{}

// WithOwner returns ctx with owner as its compilations' owner, replacing any
// owner ctx already carries.
func WithOwner(ctx context.Context, owner Owner) context.Context {
	return context.WithValue(ctx, ownerKey{}, owner)
}

// OwnerFrom returns the owner of ctx's compilations, and whether it has one.
func OwnerFrom(ctx context.Context) (Owner, bool) {
	owner, ok := ctx.Value(ownerKey{}).(Owner)
	return owner, ok
}
