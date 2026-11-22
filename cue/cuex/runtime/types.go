/*
Copyright 2022 The KubeVela Authors.

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
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"

	"github.com/kubevela/pkg/apis/cue/v1alpha1"
)

// ProviderFn the function interface to process cue values
type ProviderFn interface {
	Call(context.Context, cue.Value) (cue.Value, error)
}

// Provider the interface to get provider function
type Provider interface {
	GetName() string
	GetProviderFn(do string) ProviderFn
}

// CUETemplater the interface for retrieving cue templates and imports
type CUETemplater interface {
	GetName() string
	GetPath() string
	GetTemplates() []string
	GetImports() []*build.Instance
}

// Package composed by Provider & CUETemplater, the atomic unit for engine to use
type Package interface {
	Provider
	CUETemplater
}

// FromPackage is a provider made from a Package object, whose restrictions
// decide who may call it.
type FromPackage interface {
	// Source is the Package the provider was made from.
	Source() *v1alpha1.Package
	// GroupsOf is the $groups of the function whose #do is do.
	GroupsOf(do string) []string
}

// FileOf names a Package's CUE file uniquely across Packages, so the file a value
// came from says which Package wrote it.
func FileOf(namespace, name, file string) string {
	return filePrefix + namespace + "/" + name + "/" + file
}

// PackageOfFile is the namespace and name of the Package that wrote file, and
// whether a Package did.
func PackageOfFile(file string) (namespace, name string, ok bool) {
	rest, ok := strings.CutPrefix(file, filePrefix)
	if !ok {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

const filePrefix = "package:"
