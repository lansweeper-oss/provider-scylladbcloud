package clients

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apisCluster "github.com/lansweeper-oss/provider-scylladbcloud/apis/cluster"
	clusterscyllav1alpha1 "github.com/lansweeper-oss/provider-scylladbcloud/apis/cluster/scylladbcloud/v1alpha1"
	clusterv1beta1 "github.com/lansweeper-oss/provider-scylladbcloud/apis/cluster/v1beta1"
	apisNamespaced "github.com/lansweeper-oss/provider-scylladbcloud/apis/namespaced"
	scyllav1alpha1 "github.com/lansweeper-oss/provider-scylladbcloud/apis/namespaced/scylladbcloud/v1alpha1"
	namespacedv1beta1 "github.com/lansweeper-oss/provider-scylladbcloud/apis/namespaced/v1beta1"
)

func TestProviderConfiguration(t *testing.T) {
	cases := map[string]struct {
		creds   map[string]string
		want    map[string]any
		wantErr bool
	}{
		"TokenOnly": {
			creds: map[string]string{"token": "tok"},
			want:  map[string]any{"token": "tok"},
		},
		"TokenAndEndpoint": {
			creds: map[string]string{"token": "tok", "endpoint": "https://example.test"},
			want:  map[string]any{"token": "tok", "endpoint": "https://example.test"},
		},
		"IgnoresUnknownKeys": {
			creds: map[string]string{"token": "tok", "username": "admin"},
			want:  map[string]any{"token": "tok"},
		},
		"MissingToken": {
			creds:   map[string]string{"endpoint": "https://example.test"},
			wantErr: true,
		},
		"EmptyToken": {
			creds:   map[string]string{"token": ""},
			wantErr: true,
		},
		"WhitespaceOnlyToken": {
			creds:   map[string]string{"token": " \n"},
			wantErr: true,
		},
		// Secrets created with `echo ... | kubectl create secret` carry a
		// trailing newline, which would end up in the Authorization header.
		"TrimsSurroundingWhitespace": {
			creds: map[string]string{"token": "tok\n", "endpoint": " https://example.test\n"},
			want:  map[string]any{"token": "tok", "endpoint": "https://example.test"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := providerConfiguration(tc.creds)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got configuration %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("want %v, got %v", tc.want, got)
			}
		})
	}
}

// fakeScyllaAPI serves the endpoints the upstream provider calls while
// configuring its client, and records every path it was asked for. Requests
// without the expected bearer token get a 401, which the client doesn't retry.
func fakeScyllaAPI(t *testing.T, token string) (*httptest.Server, func() []string) {
	t.Helper()
	responses := map[string]string{
		"/deployment/scylla-versions":          `{"data":{"defaultScyllaVersionId":1,"scyllaVersions":[{"id":1,"version":"2025.1.0"}]}}`,
		"/deployment/cloud-providers":          `{"data":{"cloudProviders":[{"id":1,"name":"AWS"}]}}`,
		"/deployment/cloud-provider/1/regions": `{"data":{"regions":[],"instances":[]}}`,
		"/account/default":                     `{"data":{"accountId":7}}`,
	}
	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		body, ok := responses[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestConfigureScyllaClient(t *testing.T) {
	t.Run("BuildsClientWithUpstreamConfigure", func(t *testing.T) {
		srv, seen := fakeScyllaAPI(t, "tok")

		meta, err := configureScyllaClient(context.Background(), map[string]any{"token": "tok", "endpoint": srv.URL})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta == nil {
			t.Fatal("want a configured client, got nil")
		}

		// The upstream configure preloads deployment metadata and the account,
		// which the cluster, connection and VPC peering resources depend on.
		got := map[string]bool{}
		for _, p := range seen() {
			got[p] = true
		}
		for _, p := range []string{"/deployment/scylla-versions", "/deployment/cloud-providers", "/deployment/cloud-provider/1/regions", "/account/default"} {
			if !got[p] {
				t.Errorf("want a request to %s, requested paths: %v", p, seen())
			}
		}
	})

	t.Run("ReturnsErrorWhenAPIRejectsToken", func(t *testing.T) {
		srv, _ := fakeScyllaAPI(t, "tok")

		meta, err := configureScyllaClient(context.Background(), map[string]any{"token": "wrong", "endpoint": srv.URL})
		if err == nil {
			t.Fatalf("want error for rejected token, got client %v", meta)
		}
	})
}

// recordingConfigure captures the provider configuration it was given instead
// of calling the ScyllaDB Cloud API.
type recordingConfigure struct {
	got   map[string]any
	calls int
}

func (r *recordingConfigure) configure(_ context.Context, cfg map[string]any) (any, error) {
	r.calls++
	r.got = cfg
	return "client", nil
}

func TestTerraformSetupBuilder(t *testing.T) {
	const ns = "team-a"

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := apisNamespaced.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	newObjects := func(credentials string) []runtime.Object {
		return []runtime.Object{
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "scylla-creds", Namespace: ns},
				Data:       map[string][]byte{"credentials": []byte(credentials)},
			},
			&namespacedv1beta1.ProviderConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: ns},
				Spec: namespacedv1beta1.ProviderConfigSpec{
					Credentials: namespacedv1beta1.ProviderCredentials{
						Source: xpv2.CredentialsSourceSecret,
						CommonCredentialSelectors: xpv2.CommonCredentialSelectors{
							SecretRef: &xpv2.SecretKeySelector{
								SecretReference: xpv2.SecretReference{Name: "scylla-creds"},
								Key:             "credentials",
							},
						},
					},
				},
			},
		}
	}
	// The UID is set by the API server on real objects; the ProviderConfig
	// usage tracker names its ProviderConfigUsage after it.
	mg := &scyllav1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: ns, UID: "0b5e7a3e-5b1c-4d1e-9a4f-3f2d1c0b9a8e"},
	}
	mg.Spec.ProviderConfigReference = &xpv2.ProviderConfigReference{Kind: "ProviderConfig", Name: "default"}

	t.Run("ConfiguresClientFromSecretCredentials", func(t *testing.T) {
		kube := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(newObjects(`{"token":"tok","endpoint":"https://example.test"}`)...).Build()
		rc := &recordingConfigure{}
		setup := TerraformSetupBuilder(newClientCache(time.Minute, rc.configure, time.Now))

		ps, err := setup(context.Background(), kube, mg.DeepCopy())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := map[string]any{"token": "tok", "endpoint": "https://example.test"}
		if !reflect.DeepEqual(map[string]any(ps.Configuration), want) {
			t.Errorf("Configuration: want %v, got %v", want, ps.Configuration)
		}
		if !reflect.DeepEqual(rc.got, want) {
			t.Errorf("client configured with %v, want %v", rc.got, want)
		}
		if ps.Meta != "client" {
			t.Errorf("Meta: want the configured client, got %v", ps.Meta)
		}
	})

	t.Run("RejectsCredentialsWithoutToken", func(t *testing.T) {
		kube := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(newObjects(`{"endpoint":"https://example.test"}`)...).Build()
		rc := &recordingConfigure{}
		setup := TerraformSetupBuilder(newClientCache(time.Minute, rc.configure, time.Now))

		if _, err := setup(context.Background(), kube, mg.DeepCopy()); err == nil {
			t.Fatal("want error for credentials without token, got nil")
		}
		if rc.calls != 0 {
			t.Errorf("want no client build, got %d", rc.calls)
		}
	})
}

// Cluster-scoped managed resources resolve a cluster-scoped ProviderConfig
// (scylladbcloud.crossplane.io) and track its usage.
func TestTerraformSetupBuilderClusterScoped(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, apisCluster.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "scylla-creds", Namespace: "crossplane-system"},
			Data:       map[string][]byte{"credentials": []byte(`{"token":"tok"}`)},
		},
		&clusterv1beta1.ProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: clusterv1beta1.ProviderConfigSpec{
				Credentials: clusterv1beta1.ProviderCredentials{
					Source: xpv2.CredentialsSourceSecret,
					CommonCredentialSelectors: xpv2.CommonCredentialSelectors{
						SecretRef: &xpv2.SecretKeySelector{
							SecretReference: xpv2.SecretReference{Name: "scylla-creds", Namespace: "crossplane-system"},
							Key:             "credentials",
						},
					},
				},
			},
		},
	).Build()

	mg := &clusterscyllav1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", UID: "4c1f8d2e-7a3b-4e5f-9c0d-1b2a3c4d5e6f"}}
	mg.Spec.ProviderConfigReference = &xpv2.Reference{Name: "default"}

	rc := &recordingConfigure{}
	ps, err := TerraformSetupBuilder(newClientCache(time.Minute, rc.configure, time.Now))(context.Background(), kube, mg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := map[string]any{"token": "tok"}; !reflect.DeepEqual(map[string]any(ps.Configuration), want) {
		t.Errorf("Configuration: want %v, got %v", want, ps.Configuration)
	}
	if ps.Meta != "client" {
		t.Errorf("Meta: want the configured client, got %v", ps.Meta)
	}

	usages := &clusterv1beta1.ProviderConfigUsageList{}
	if err := kube.List(context.Background(), usages); err != nil {
		t.Fatal(err)
	}
	if len(usages.Items) != 1 {
		t.Errorf("want 1 ProviderConfigUsage tracking the managed resource, got %d", len(usages.Items))
	}
}
