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

package access

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevela/pkg/apis/cue/v1alpha1"
	"github.com/kubevela/pkg/cue/cuex"
)

// VerbCall is the RBAC verb a grant on a Package gives.
const VerbCall = "call"

// KubeAuthorizer answers from the cluster: bindings and namespaces through
// Reader, which a controller gives as its cached client so membership is read
// from informers, and grants with a SubjectAccessReview created through
// Reviewer, remembered for TTL.
type KubeAuthorizer struct {
	Reader   client.Reader
	Reviewer client.Client
	TTL      time.Duration
	// Now is the clock answers age by; time.Now when nil.
	Now func() time.Time

	mu   sync.Mutex
	memo map[string]remembered
}

type remembered struct {
	allowed bool
	at      time.Time
}

var _ Authorizer = &KubeAuthorizer{}

// Member implements Authorizer.
func (k *KubeAuthorizer) Member(ctx context.Context, w cuex.Writer, roles []v1alpha1.RoleRef, namespace string) (bool, error) {
	return Member(ctx, k.Reader, w, roles, namespace)
}

// MayCall implements Authorizer, asking for each name in turn until one is
// allowed.
func (k *KubeAuthorizer) MayCall(ctx context.Context, w cuex.Writer, namespace string, names []string) (bool, error) {
	key := w.User + "|" + strings.Join(w.Groups, ",") + "|" + namespace + "|" + strings.Join(names, ",")
	now := k.now()
	k.mu.Lock()
	if r, ok := k.memo[key]; ok && now.Sub(r.at) < k.TTL {
		k.mu.Unlock()
		return r.allowed, nil
	}
	k.mu.Unlock()
	allowed := false
	for _, name := range names {
		review := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{
			User: w.User, Groups: w.Groups,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Verb: VerbCall, Group: v1alpha1.Group, Resource: "packages", Name: name, Namespace: namespace,
			},
		}}
		if err := k.Reviewer.Create(ctx, review); err != nil {
			return false, fmt.Errorf("cannot review whether %s may call %s in %s: %w", w.User, name, namespace, err)
		}
		if review.Status.Allowed {
			allowed = true
			break
		}
	}
	k.mu.Lock()
	if k.memo == nil {
		k.memo = map[string]remembered{}
	}
	for key, r := range k.memo {
		if now.Sub(r.at) >= k.TTL {
			delete(k.memo, key)
		}
	}
	k.memo[key] = remembered{allowed: allowed, at: now}
	k.mu.Unlock()
	return allowed, nil
}

// NamespaceLabels implements Authorizer. A namespace that does not exist has no
// labels.
func (k *KubeAuthorizer) NamespaceLabels(ctx context.Context, namespace string) (map[string]string, error) {
	ns := &corev1.Namespace{}
	if err := k.Reader.Get(ctx, client.ObjectKey{Name: namespace}, ns); err != nil {
		if kerrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read namespace %s: %w", namespace, err)
	}
	return ns.Labels, nil
}

func (k *KubeAuthorizer) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// Member reports whether w is bound to any of roles by a ClusterRoleBinding, or
// by a RoleBinding in namespace. Membership is by name: a binding to another
// role, however wide, does not count.
func Member(ctx context.Context, reader client.Reader, w cuex.Writer, roles []v1alpha1.RoleRef, namespace string) (bool, error) {
	crbs := &rbacv1.ClusterRoleBindingList{}
	if err := reader.List(ctx, crbs); err != nil {
		return false, fmt.Errorf("cannot list ClusterRoleBindings to check role membership: %w", err)
	}
	for _, b := range crbs.Items {
		if wants(roles, b.RoleRef) && boundTo(w, b.Subjects) {
			return true, nil
		}
	}
	rbs := &rbacv1.RoleBindingList{}
	if err := reader.List(ctx, rbs, client.InNamespace(namespace)); err != nil {
		return false, fmt.Errorf("cannot list RoleBindings in %q to check role membership: %w", namespace, err)
	}
	for _, b := range rbs.Items {
		if wants(roles, b.RoleRef) && boundTo(w, b.Subjects) {
			return true, nil
		}
	}
	return false, nil
}

func wants(roles []v1alpha1.RoleRef, ref rbacv1.RoleRef) bool {
	for _, r := range roles {
		if r.Kind == ref.Kind && r.Name == ref.Name {
			return true
		}
	}
	return false
}

// boundTo reports whether subjects name w, as RBAC matches them.
func boundTo(w cuex.Writer, subjects []rbacv1.Subject) bool {
	for _, s := range subjects {
		switch s.Kind {
		case rbacv1.UserKind:
			if s.Name == w.User {
				return true
			}
		case rbacv1.GroupKind:
			for _, g := range w.Groups {
				if g == s.Name {
					return true
				}
			}
		case rbacv1.ServiceAccountKind:
			if w.User == "system:serviceaccount:"+s.Namespace+":"+s.Name {
				return true
			}
		}
	}
	return false
}
