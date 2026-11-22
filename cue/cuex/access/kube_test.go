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

package access_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/kubevela/pkg/apis/cue/v1alpha1"
	"github.com/kubevela/pkg/cue/cuex"
	"github.com/kubevela/pkg/cue/cuex/access"
)

func TestMember(t *testing.T) {
	ref := func(kind, name string) rbacv1.RoleRef {
		return rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: kind, Name: name}
	}
	user := func(n string) rbacv1.Subject { return rbacv1.Subject{Kind: rbacv1.UserKind, Name: n} }
	group := func(n string) rbacv1.Subject { return rbacv1.Subject{Kind: rbacv1.GroupKind, Name: n} }
	sa := rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Namespace: "argocd", Name: "platform"}
	cli := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "leads"}, RoleRef: ref("ClusterRole", "admins"), Subjects: []rbacv1.Subject{group("leads")}},
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "wide"}, RoleRef: ref("ClusterRole", "cluster-admin"), Subjects: []rbacv1.Subject{user("root")}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "carol", Namespace: system}, RoleRef: ref("ClusterRole", "admins"), Subjects: []rbacv1.Subject{user("carol"), sa}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "dave", Namespace: "team-a"}, RoleRef: ref("ClusterRole", "admins"), Subjects: []rbacv1.Subject{user("dave")}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "frank", Namespace: system}, RoleRef: ref("Role", "admins"), Subjects: []rbacv1.Subject{user("frank")}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "gina", Namespace: system}, RoleRef: ref("Role", "callers"), Subjects: []rbacv1.Subject{user("gina")}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "hugh", Namespace: "team-a"}, RoleRef: ref("Role", "callers"), Subjects: []rbacv1.Subject{user("hugh")}},
	).Build()
	admins := []v1alpha1.RoleRef{{Kind: "ClusterRole", Name: "admins"}}
	callers := []v1alpha1.RoleRef{{Kind: "Role", Name: "callers"}}
	for name, tc := range map[string]struct {
		w     cuex.Writer
		roles []v1alpha1.RoleRef
		want  bool
	}{
		"a group bound cluster-wide":                 {cuex.Writer{User: "alice", Groups: []string{"leads"}}, admins, true},
		"a user bound where the object lives":        {cuex.Writer{User: "carol"}, admins, true},
		"a service account bound there":              {cuex.Writer{User: "system:serviceaccount:argocd:platform"}, admins, true},
		"a binding in another namespace":             {cuex.Writer{User: "dave"}, admins, false},
		"a binding to another role, however wide":    {cuex.Writer{User: "root"}, admins, false},
		"a Role of the same name as the ClusterRole": {cuex.Writer{User: "frank"}, admins, false},
		"a Role bound where the object lives":        {cuex.Writer{User: "gina"}, callers, true},
		"a Role of that name in another namespace":   {cuex.Writer{User: "hugh"}, callers, false},
		"no binding": {cuex.Writer{User: "bob"}, admins, false},
	} {
		t.Run(name, func(t *testing.T) {
			ok, err := access.Member(context.Background(), cli, tc.w, tc.roles, system)
			require.NoError(t, err)
			require.Equal(t, tc.want, ok)
		})
	}
}

func TestKubeAuthorizer(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	var reviews []authorizationv1.SubjectAccessReviewSpec
	cli := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team-a", Labels: map[string]string{"github-write": "true"}}}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				review, ok := obj.(*authorizationv1.SubjectAccessReview)
				if !ok {
					return c.Create(ctx, obj, opts...)
				}
				reviews = append(reviews, review.Spec)
				a := review.Spec.ResourceAttributes
				review.Status.Allowed = slices.Contains(review.Spec.Groups, "payments-leads") &&
					a.Verb == access.VerbCall && a.Group == "cue.oam.dev" && a.Resource == "packages" &&
					a.Name == "example.com/githubwrite#group:issues" && a.Namespace == system
				return nil
			},
		}).Build()
	now := time.Unix(0, 0)
	auth := &access.KubeAuthorizer{Reader: cli, Reviewer: cli, TTL: time.Minute, Now: func() time.Time { return now }}
	ctx := context.Background()
	lead := cuex.Writer{User: "erin", Groups: []string{"payments-leads"}}
	names := []string{"example.com/githubwrite", "example.com/githubwrite#group:issues", "example.com/githubwrite#issues/create"}

	t.Run("a grant on any of the names will do, asked in the namespace given", func(t *testing.T) {
		ok, err := auth.MayCall(ctx, lead, system, names)
		require.NoError(t, err)
		require.True(t, ok)
		ok, err = auth.MayCall(ctx, cuex.Writer{User: "bob"}, system, names)
		require.NoError(t, err)
		require.False(t, ok)
	})
	t.Run("an answer is remembered until it is older than the TTL", func(t *testing.T) {
		before := len(reviews)
		_, err := auth.MayCall(ctx, lead, system, names)
		require.NoError(t, err)
		require.Equal(t, before, len(reviews), "asked again within the TTL")
		now = now.Add(2 * time.Minute)
		_, err = auth.MayCall(ctx, lead, system, names)
		require.NoError(t, err)
		require.Greater(t, len(reviews), before, "not asked again after the TTL")
	})
	t.Run("an answer is kept to the writer, groups and namespace it was given for", func(t *testing.T) {
		_, err := auth.MayCall(ctx, lead, system, names)
		require.NoError(t, err)
		for _, ask := range []struct {
			name string
			w    cuex.Writer
			ns   string
			want bool
		}{
			{"another user in the same groups", cuex.Writer{User: "bob", Groups: lead.Groups}, system, true},
			{"the same user in fewer groups", cuex.Writer{User: lead.User}, system, false},
			{"another namespace", lead, "team-a", false},
		} {
			before := len(reviews)
			ok, err := auth.MayCall(ctx, ask.w, ask.ns, names)
			require.NoError(t, err, ask.name)
			require.Greater(t, len(reviews), before, "%s answered from another's memo", ask.name)
			require.Equal(t, ask.want, ok, ask.name)
		}
	})
	t.Run("a review the cluster cannot answer is an error, and is not remembered", func(t *testing.T) {
		var tries int
		failing := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				tries++
				return errors.New("connection refused")
			},
		}).Build()
		down := &access.KubeAuthorizer{Reader: failing, Reviewer: failing, TTL: time.Minute, Now: func() time.Time { return now }}
		for range 2 {
			ok, err := down.MayCall(ctx, lead, system, names)
			require.ErrorContains(t, err, "connection refused")
			require.False(t, ok)
		}
		require.Equal(t, 2, tries, "a failure was answered from the memo")
	})
	t.Run("a namespace's labels", func(t *testing.T) {
		labels, err := auth.NamespaceLabels(ctx, "team-a")
		require.NoError(t, err)
		require.Equal(t, "true", labels["github-write"])
		labels, err = auth.NamespaceLabels(ctx, "missing")
		require.NoError(t, err)
		require.Nil(t, labels)
	})
}
