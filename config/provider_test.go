package config

import (
	"reflect"
	"testing"

	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
)

// expectedKinds maps every Terraform resource to the Kind it must have in the
// single "scylladbcloud" short group.
var expectedKinds = map[string]string{
	"scylladbcloud_allowlist_rule":     "AllowListRule",
	"scylladbcloud_cluster":            "Cluster",
	"scylladbcloud_cluster_connection": "ClusterConnection",
	"scylladbcloud_serverless_cluster": "ServerlessCluster",
	"scylladbcloud_vpc_peering":        "VpcPeering",
}

// The upstream VPC peering docs still use peer_cidr_block, which the provider
// replaced with the peer_cidr_blocks list; generated examples must use the
// current field or they fail schema validation.
func TestVpcPeeringExamplesUseCurrentCIDRField(t *testing.T) {
	for scope, p := range map[string]*ujconfig.Provider{"Cluster": GetProvider(), "Namespaced": GetProviderNamespaced()} {
		t.Run(scope, func(t *testing.T) {
			examples := p.Resources["scylladbcloud_vpc_peering"].MetaResource.Examples
			if len(examples) == 0 {
				t.Fatal("no examples for scylladbcloud_vpc_peering")
			}
			for i, e := range examples {
				params := e.Paved.UnstructuredContent()
				if _, ok := params["peer_cidr_block"]; ok {
					t.Errorf("example %d still has peer_cidr_block: %v", i, params)
				}
			}
			got := examples[0].Paved.UnstructuredContent()["peer_cidr_blocks"]
			want := []any{"192.168.0.0/16"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("example 0 peer_cidr_blocks: want %v, got %v", want, got)
			}
		})
	}
}

func TestResourceGroupsAndKinds(t *testing.T) {
	providers := map[string]struct {
		provider  *ujconfig.Provider
		rootGroup string
	}{
		"Cluster":    {provider: GetProvider(), rootGroup: "scylladbcloud.crossplane.io"},
		"Namespaced": {provider: GetProviderNamespaced(), rootGroup: "scylladbcloud.m.crossplane.io"},
	}

	for scope, tc := range providers {
		t.Run(scope, func(t *testing.T) {
			if tc.provider.RootGroup != tc.rootGroup {
				t.Errorf("RootGroup: want %q, got %q", tc.rootGroup, tc.provider.RootGroup)
			}
			if len(tc.provider.Resources) != len(expectedKinds) {
				t.Errorf("want %d resources, got %d", len(expectedKinds), len(tc.provider.Resources))
			}
			for name, kind := range expectedKinds {
				r, ok := tc.provider.Resources[name]
				if !ok {
					t.Errorf("resource %q not found", name)
					continue
				}
				if r.ShortGroup != resourcePrefix {
					t.Errorf("%s ShortGroup: want %q, got %q", name, resourcePrefix, r.ShortGroup)
				}
				if r.Kind != kind {
					t.Errorf("%s Kind: want %q, got %q", name, kind, r.Kind)
				}
				if r.TerraformResource == nil {
					t.Errorf("%s: TerraformResource is nil, want the in-process SDKv2 resource", name)
				}
			}
		})
	}
}
