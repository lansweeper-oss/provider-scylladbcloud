package config

import (
	"github.com/crossplane/upjet/v2/pkg/config"
)

// ExternalNameConfigs contains all external name configurations for this
// provider.
//
// Every resource gets its ID from the provider on create, and its Terraform
// read looks the object up by that bare ID alone, so the external name is the
// provider-assigned ID.
var ExternalNameConfigs = map[string]config.ExternalName{
	// Numeric cluster ID
	"scylladbcloud_cluster": config.IdentifierFromProvider,
	// Numeric connection ID
	"scylladbcloud_cluster_connection": config.IdentifierFromProvider,
	// Numeric allowlist rule ID; read searches all clusters for it
	"scylladbcloud_allowlist_rule": config.IdentifierFromProvider,
	// Numeric serverless cluster ID
	"scylladbcloud_serverless_cluster": config.IdentifierFromProvider,
	// Key ID part of the API token ("<id>:<secret>"); read is a no-op upstream
	"scylladbcloud_stack": config.IdentifierFromProvider,
	// Cloud-side peering ID ("externalId" in the API); read searches all
	// clusters for it
	"scylladbcloud_vpc_peering": config.IdentifierFromProvider,
}

// ExternalNameConfigurations applies all external name configs listed in the
// table ExternalNameConfigs and sets the version of those resources to v1beta1
// assuming they will be tested.
func ExternalNameConfigurations() config.ResourceOption {
	return func(r *config.Resource) {
		if e, ok := ExternalNameConfigs[r.Name]; ok {
			r.ExternalName = e
		}
	}
}

// ExternalNameConfigured returns the list of all resources whose external name
// is configured manually.
func ExternalNameConfigured() []string {
	l := make([]string, len(ExternalNameConfigs))
	i := 0
	for name := range ExternalNameConfigs {
		// $ is added to match the exact string since the format is regex.
		l[i] = name + "$"
		i++
	}
	return l
}
