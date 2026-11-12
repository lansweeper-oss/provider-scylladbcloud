package main

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	apisCluster "github.com/lansweeper-oss/provider-scylladbcloud/apis/cluster"
	apisNamespaced "github.com/lansweeper-oss/provider-scylladbcloud/apis/namespaced"
	"github.com/lansweeper-oss/provider-scylladbcloud/config"
)

// Every provider group must reach the CRD gate, otherwise its controllers never start.
func TestProviderGroupsCoversAllProviderTypes(t *testing.T) {
	providerOnly := runtime.NewScheme()
	if err := apisCluster.AddToScheme(providerOnly); err != nil {
		t.Fatal(err)
	}
	if err := apisNamespaced.AddToScheme(providerOnly); err != nil {
		t.Fatal(err)
	}

	full := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, apisCluster.AddToScheme, apisNamespaced.AddToScheme} {
		if err := add(full); err != nil {
			t.Fatal(err)
		}
	}

	want := map[string]bool{}
	for gvk := range providerOnly.AllKnownTypes() {
		if gvk.Group != "" { // metav1 types registered in the core group, no CRD
			want[gvk.Group] = true
		}
	}

	got := providerGroups(full, config.GetProvider().RootGroup, config.GetProviderNamespaced().RootGroup)
	if len(got) != len(want) {
		t.Fatalf("want %d groups %v, got %d groups %v", len(want), want, len(got), got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected group %q", g)
		}
	}
}

// A zero or negative TTL would silently disable the client cache and rebuild
// the client (several API calls) on every reconcile.
func TestValidateClientCacheTTL(t *testing.T) {
	for ttl, wantErr := range map[time.Duration]bool{0: true, -time.Second: true, time.Nanosecond: false, 30 * time.Minute: false} {
		err := validateClientCacheTTL(ttl)
		if (err != nil) != wantErr {
			t.Errorf("validateClientCacheTTL(%v): want error %v, got %v", ttl, wantErr, err)
		}
	}
}
