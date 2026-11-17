package clients

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/crossplane/upjet/v2/pkg/diffserver"
	"github.com/scylladb/terraform-provider-scylladbcloud/pkg/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apisCluster "github.com/lansweeper-oss/provider-scylladbcloud/apis/cluster"
	clusterscyllav1alpha1 "github.com/lansweeper-oss/provider-scylladbcloud/apis/cluster/scylladbcloud/v1alpha1"
	clusterv1beta1 "github.com/lansweeper-oss/provider-scylladbcloud/apis/cluster/v1beta1"
)

// recordingTransport records every request the client tries to send and
// refuses it, standing in for the network.
type recordingTransport struct {
	mu       sync.Mutex
	requests []string
}

var errRecorded = errors.New("request recorded, not sent")

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requests = append(t.requests, req.Method+" "+req.URL.Host+req.URL.Path)
	return nil, errRecorded
}

// readAll runs the Terraform read of every resource the Crossplane provider
// exposes with the given meta. Reads call the ScyllaDB Cloud API, so they show
// where the client's requests go.
func readAll(t *testing.T, meta any) map[string]string {
	t.Helper()
	ctx := context.Background()
	p := provider.New(ctx)
	errs := map[string]string{}
	for _, name := range []string{
		"scylladbcloud_cluster",
		"scylladbcloud_allowlist_rule",
		"scylladbcloud_cluster_connection",
		"scylladbcloud_serverless_cluster",
		"scylladbcloud_vpc_peering",
	} {
		r := p.ResourcesMap[name]
		d := r.TestResourceData()
		d.SetId("123")
		if err := d.Set("cluster_id", 123); err != nil {
			t.Fatalf("%s: cannot set cluster_id: %v", name, err)
		}
		diags := r.ReadContext(ctx, d, meta)
		if diags.HasError() {
			errs[name] = diagnosticsError(diags).Error()
		}
	}
	return errs
}

func TestNewOfflineClientSendsEveryRequestThroughTheTransport(t *testing.T) {
	rt := &recordingTransport{}
	meta, err := provider.NewOfflineClient(rt)
	if err != nil {
		t.Fatalf("cannot build the offline client: %v", err)
	}

	errs := readAll(t, meta)

	if len(rt.requests) == 0 {
		t.Fatal("want the reads to try the API through the transport, got no request")
	}
	for _, r := range rt.requests {
		if !strings.Contains(r, "scylladbcloud.offline.invalid") {
			t.Errorf("request %q targets a real host, want the offline placeholder", r)
		}
	}
	if len(errs) != 5 {
		t.Errorf("want every read to fail without the API, got errors for %d resources: %v", len(errs), errs)
	}
}

func TestOfflineTransportRefusesEveryRequest(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.cloud.scylladb.com/account/default?token=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	rsp, err := offlineTransport{}.RoundTrip(req)
	if rsp != nil {
		_ = rsp.Body.Close()
		t.Errorf("want no response, got %v", rsp)
	}
	if !diffserver.IsDiffComputationNotSupportedError(err) {
		t.Errorf("want a diff computation not supported error, got %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("the error leaks the query string: %v", err)
	}

	// Through the real client and resources the refusal surfaces as the
	// read's error.
	meta, err := provider.NewOfflineClient(offlineTransport{})
	if err != nil {
		t.Fatalf("cannot build the offline client: %v", err)
	}
	for name, msg := range readAll(t, meta) {
		if !strings.Contains(msg, "offline diff server blocked") {
			t.Errorf("%s: want the offline refusal, got %q", name, msg)
		}
	}
}

func TestOfflineTerraformSetupBuilder(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := apisCluster.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	// The ProviderConfig references a Secret that exists nowhere: the offline
	// setup must never read credentials.
	pc := &clusterv1beta1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: clusterv1beta1.ProviderConfigSpec{
			Credentials: clusterv1beta1.ProviderCredentials{
				Source: xpv2.CredentialsSourceSecret,
				CommonCredentialSelectors: xpv2.CommonCredentialSelectors{
					SecretRef: &xpv2.SecretKeySelector{
						SecretReference: xpv2.SecretReference{Name: "missing", Namespace: "crossplane-system"},
						Key:             "credentials",
					},
				},
			},
		},
	}
	mg := func(pcName string) *clusterscyllav1alpha1.Cluster {
		m := &clusterscyllav1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", UID: "8d6f1c2a-0b3e-4f5a-9c7d-2e1f0a9b8c7d"}}
		m.Spec.ProviderConfigReference = &xpv2.Reference{Name: pcName}
		return m
	}

	builds := 0
	newClient := func(rt http.RoundTripper) (any, error) {
		builds++
		if _, ok := rt.(offlineTransport); !ok {
			t.Errorf("want the client built with offlineTransport, got %T", rt)
		}
		return "offline-client", nil
	}
	setup := OfflineTerraformSetupBuilder(newClient)

	t.Run("ResolvesTheProviderConfigWithoutReadingCredentials", func(t *testing.T) {
		kube := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pc.DeepCopy()).Build()
		for range 2 {
			ps, err := setup(context.Background(), kube, mg("default"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ps.Meta != "offline-client" {
				t.Errorf("Meta: want the offline client, got %v", ps.Meta)
			}
			if tok, _ := ps.Configuration[keyToken].(string); tok == "" {
				t.Errorf("Configuration: want a placeholder token, got %v", ps.Configuration)
			}
		}
		if builds != 1 {
			t.Errorf("want the offline client built once for the process, got %d builds", builds)
		}
	})

	t.Run("FailsWhenTheProviderConfigIsMissing", func(t *testing.T) {
		kube := fake.NewClientBuilder().WithScheme(scheme).Build()
		if _, err := setup(context.Background(), kube, mg("missing")); err == nil || !strings.Contains(err.Error(), errGetProviderConfig) {
			t.Errorf("want a %q error, got %v", errGetProviderConfig, err)
		}
	})
}
