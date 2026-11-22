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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Package is an extension for cuex engine
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:printcolumn:name="PATH",type=string,JSONPath=`.spec.path`
// +kubebuilder:printcolumn:name="PROTO",type=string,JSONPath=`.spec.provider.protocol`
// +kubebuilder:printcolumn:name="ENDPOINT",type=string,JSONPath=`.spec.provider.endpoint`
// +kubebuilder:resource:shortName={pkg,cpkg,cuepkg,cuepackage}
type Package struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PackageSpec `json:"spec"`
}

// PackageSpec the spec for Package
type PackageSpec struct {
	Path      string            `json:"path"`
	Provider  *Provider         `json:"provider,omitempty"`
	Templates map[string]string `json:"templates"`
	// Restrictions limits who may call the package's functions. Absent means
	// anyone who can import it.
	// +optional
	Restrictions *PackageRestrictions `json:"restrictions,omitempty"`
}

// PackageRestrictions limits the calls to a package's functions by the namespace
// of whatever makes the call, a definition or a Package, and by who last wrote it.
type PackageRestrictions struct {
	// CallRule applies to every call to the package.
	CallRule `json:",inline"`

	// Functions narrow the rule for some functions. For each of a function's
	// $groups, the first entry matching the function or that group applies; a
	// function in no group takes the first entry matching its name.
	// +optional
	Functions []FunctionRestriction `json:"functions,omitempty"`
}

// FunctionRestriction is a rule for the functions it matches.
type FunctionRestriction struct {
	// Match lists functions by their #do, or a group as group:<name>. Empty
	// matches every function.
	// +optional
	Match []string `json:"match,omitempty"`

	CallRule `json:",inline"`
}

// CallRule is who may make a call. Every field that is set must pass.
type CallRule struct {
	// Namespaces the caller's namespace must match, by name or path.Match glob.
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`

	// NamespaceSelector the caller's namespace labels must match.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// RequireRoles are roles any one of whose members may call. The caller's
	// writer is a member when a ClusterRoleBinding, or a RoleBinding in the
	// Package's namespace, binds the role to its user, one of its groups or its
	// service account.
	// +optional
	RequireRoles []RoleRef `json:"requireRoles,omitempty"`

	// RequireGrant requires the caller's writer to hold the verb "call" on
	// packages in cue.oam.dev, named for the package, the function's group or the
	// function, asked in the Package's namespace.
	// +optional
	RequireGrant bool `json:"requireGrant,omitempty"`
}

// RoleRef names a ClusterRole, or a Role in the restricted object's namespace.
type RoleRef struct {
	// +kubebuilder:validation:Enum=Role;ClusterRole
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// String is the role as Kind/name.
func (r RoleRef) String() string { return r.Kind + "/" + r.Name }

// ProviderProtocol the protocol type for external Provider
type ProviderProtocol string

const (
	// ProtocolGRPC protocol type grpc for external Provider
	ProtocolGRPC ProviderProtocol = "grpc"
	// ProtocolHTTP protocol type http for external Provider
	ProtocolHTTP ProviderProtocol = "http"
	// ProtocolHTTPS protocol type https for external Provider
	ProtocolHTTPS ProviderProtocol = "https"
)

// Provider the external Provider in Package for cuex to run functions
type Provider struct {
	Protocol ProviderProtocol `json:"protocol"`
	Endpoint string           `json:"endpoint"`
	// +optional
	// +kubebuilder:default={}
	Header map[string]string `json:"header,omitempty"`
}

// PackageList list for Package
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PackageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Package `json:"items"`
}
