package config

import (
	"strings"

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
	p.AddResourceConfigurator("scylladbcloud_vpc_peering", migrateExamplePeerCIDRBlock)
	p.AddResourceConfigurator("scylladbcloud_cluster", func(r *config.Resource) {
		r.Sensitive.AdditionalConnectionDetailsFn = cqlConnectionDetails
		// The upstream docs don't cover this attribute, which the provider shim adds,
		// so Upjet would otherwise borrow the doc of its "name" suffix.
		r.MetaResource.ArgumentDocs["cql_username"] = r.TerraformResource.Schema["cql_username"].Description
	})
}

// cqlConnectionDetails publishes what a CQL client needs under plain keys, on top of the
// attribute.cql_password key Upjet adds for the sensitive attribute.
// That's the output of the scylladbcloud_cql_auth data source, plus the CA certificate for TLS connections.
// Empty values are skipped, e.g. ca.crt when in-transit encryption is disabled.
func cqlConnectionDetails(attr map[string]any) (map[string][]byte, error) {
	names, _ := attr["node_dns_names"].([]any)
	var seeds []string
	for _, n := range names {
		// Nodes without a DNS name report "", which must not end up as an empty seed.
		if s, _ := n.(string); s != "" {
			seeds = append(seeds, s)
		}
	}
	conn := map[string][]byte{}
	for key, v := range map[string]any{
		"username": attr["cql_username"],
		"password": attr["cql_password"],
		"ca.crt":   attr["ca_certificate"],
		"seeds":    strings.Join(seeds, ","),
	} {
		if s, _ := v.(string); s != "" {
			conn[key] = []byte(s)
		}
	}
	return conn, nil
}

// migrateExamplePeerCIDRBlock rewrites the scraped examples, which come from
// upstream docs that still use the removed peer_cidr_block string, to the
// current peer_cidr_blocks list. Otherwise the generated examples fail schema
// validation.
func migrateExamplePeerCIDRBlock(r *config.Resource) {
	if r.MetaResource == nil {
		return
	}
	for i := range r.MetaResource.Examples {
		e := &r.MetaResource.Examples[i]
		cidr, err := e.Paved.GetValue("peer_cidr_block")
		if err != nil {
			continue // field not present in this example
		}
		if err := e.Paved.DeleteField("peer_cidr_block"); err != nil {
			panic(err)
		}
		if err := e.SetPathValue("peer_cidr_blocks", []any{cidr}); err != nil {
			panic(err)
		}
	}
}
