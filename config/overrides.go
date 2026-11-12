package config

import (
	"github.com/crossplane/upjet/v2/pkg/config"
)

// kindOverrides places every resource in the single "scylladbcloud" short
// group. Upjet's default would split multi-word names into their own groups,
// e.g. scylladbcloud_vpc_peering => vpc.scylladbcloud.crossplane.io/Peering.
var kindOverrides = map[string]string{
	"scylladbcloud_allowlist_rule":     "AllowListRule",
	"scylladbcloud_cluster_connection": "ClusterConnection",
	"scylladbcloud_serverless_cluster": "ServerlessCluster",
	"scylladbcloud_vpc_peering":        "VpcPeering",
}

// Configure adds resource-specific overrides to the provider configuration.
//
// cluster_id is deliberately not configured as a cross-resource reference: it
// is a Terraform TypeInt (Go *int64), and crossplane-tools only generates
// reference resolvers for string and float64 fields.
func Configure(p *config.Provider) {
	for name, kind := range kindOverrides {
		p.AddResourceConfigurator(name, func(r *config.Resource) {
			r.ShortGroup = resourcePrefix
			r.Kind = kind
		})
	}
}
