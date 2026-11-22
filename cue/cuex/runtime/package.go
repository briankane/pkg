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
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/literal"

	"github.com/kubevela/pkg/apis/cue/v1alpha1"
	"github.com/kubevela/pkg/cue/util"
	"github.com/kubevela/pkg/util/maps"
)

const (
	// VelaPrefix default prefix for internal package names
	VelaPrefix = "vela/"
)

type internalPackage struct {
	name     string
	template string
	imp      *build.Instance
	fns      *maps.SyncMap[string, ProviderFn]
}

func (in *internalPackage) GetProviderFn(do string) ProviderFn {
	fn, _ := in.fns.Get(do)
	return fn
}

func (in *internalPackage) GetName() string {
	return in.name
}

func (in *internalPackage) GetPath() string {
	return VelaPrefix + in.name
}

func (in *internalPackage) GetTemplates() []string {
	return []string{in.template}
}

func (in *internalPackage) GetImports() []*build.Instance {
	return []*build.Instance{in.imp}
}

var _ Package = &internalPackage{}

// NewInternalPackage create package based on given functions
func NewInternalPackage(name string, template string, fns map[string]ProviderFn) (Package, error) {
	pkg := &internalPackage{name: name, template: template}
	bi, err := util.BuildImport(pkg.GetPath(), map[string]string{"-": template})
	if err != nil {
		return nil, err
	}
	pkg.imp = bi
	pkg.fns = maps.NewSyncMapFrom(fns)
	return pkg, nil
}

type externalPackage struct {
	src     *v1alpha1.Package
	imports []*build.Instance
	groups  map[string][]string
}

// Source implements FromPackage.
func (in *externalPackage) Source() *v1alpha1.Package { return in.src }

// GroupsOf implements FromPackage.
func (in *externalPackage) GroupsOf(do string) []string { return in.groups[do] }

func (in *externalPackage) GetProviderFn(do string) ProviderFn {
	if in.src.Spec.Provider == nil {
		return nil
	}
	// An external call is a request to an endpoint and holds nothing shared
	// with its siblings, so a template asking for @concurrency gets it, up to
	// whatever ceiling the function's own definition declares.
	return Concurrent(&ExternalProviderFn{
		Provider: *in.src.Spec.Provider,
		Fn:       do,
	})
}

func (in *externalPackage) GetName() string {
	return in.src.Name
}

func (in *externalPackage) GetPath() string {
	return in.src.Spec.Path
}

func (in *externalPackage) GetTemplates() []string {
	return maps.Values(in.src.Spec.Templates)
}

func (in *externalPackage) GetImports() []*build.Instance {
	return in.imports
}

var _ Package = &externalPackage{}

var _ FromPackage = &externalPackage{}

// NewExternalPackage create Package based on given CRD object. Its files are
// named by FileOf, so a value's position says which Package wrote it.
func NewExternalPackage(src *v1alpha1.Package) (Package, error) {
	pkg := &externalPackage{src: src}
	templates := make(map[string]string, len(src.Spec.Templates))
	for file, template := range src.Spec.Templates {
		templates[FileOf(src.Namespace, src.Name, file)] = template
	}
	bi, err := util.BuildImport(pkg.GetPath(), templates)
	if err != nil {
		return nil, err
	}
	pkg.imports = []*build.Instance{bi}
	pkg.groups = groupsOf(bi.Files)
	return pkg, nil
}

// groupsOf reads each function's $groups, by its #do, from the top-level
// definitions of a Package's files. A $groups that is not a list of string
// literals is ignored.
func groupsOf(files []*ast.File) map[string][]string {
	out := map[string][]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			field, ok := d.(*ast.Field)
			if !ok {
				continue
			}
			st, ok := field.Value.(*ast.StructLit)
			if !ok {
				continue
			}
			var do string
			var groups []string
			for _, el := range st.Elts {
				inner, ok := el.(*ast.Field)
				if !ok {
					continue
				}
				switch label, _, _ := ast.LabelName(inner.Label); label {
				case "#do":
					if lit, ok := inner.Value.(*ast.BasicLit); ok {
						do, _ = literal.Unquote(lit.Value)
					}
				case "$groups":
					if list, ok := inner.Value.(*ast.ListLit); ok {
						for _, x := range list.Elts {
							if lit, ok := x.(*ast.BasicLit); ok {
								if g, err := literal.Unquote(lit.Value); err == nil {
									groups = append(groups, g)
								}
							}
						}
					}
				}
			}
			if do != "" && len(groups) > 0 {
				out[do] = groups
			}
		}
	}
	return out
}
