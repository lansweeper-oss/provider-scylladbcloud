package config

import (
	// Note(turkenh): we are importing this to embed provider schema document
	_ "embed"

	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

const (
	resourcePrefix = "scylladbcloud"
	modulePath     = "github.com/lansweeper-oss/provider-scylladbcloud"
)

//go:embed schema.json
var providerSchema string

//go:embed provider-metadata.yaml
var providerMetadata string

// GetProvider returns the cluster-scoped provider configuration over the given
// upstream provider instance. Both scopes can share one instance: building them
// leaves its resource schemas untouched.
func GetProvider(tfProvider *schema.Provider) *ujconfig.Provider {
	pc := ujconfig.NewProvider([]byte(providerSchema), resourcePrefix, modulePath, []byte(providerMetadata),
		ujconfig.WithRootGroup("scylladbcloud.crossplane.io"),
		ujconfig.WithIncludeList(nil),
		ujconfig.WithTerraformPluginSDKIncludeList(ExternalNameConfigured()),
		ujconfig.WithTerraformProvider(tfProvider),
		ujconfig.WithFeaturesPackage("internal/features"),
		ujconfig.WithDefaultResourceOptions(
			ExternalNameConfigurations(),
		))

	Configure(pc)

	pc.ConfigureResources()
	return pc
}

// GetProviderNamespaced returns the namespaced provider configuration over the
// given upstream provider instance.
func GetProviderNamespaced(tfProvider *schema.Provider) *ujconfig.Provider {
	pc := ujconfig.NewProvider([]byte(providerSchema), resourcePrefix, modulePath, []byte(providerMetadata),
		ujconfig.WithRootGroup("scylladbcloud.m.crossplane.io"),
		ujconfig.WithIncludeList(nil),
		ujconfig.WithTerraformPluginSDKIncludeList(ExternalNameConfigured()),
		ujconfig.WithTerraformProvider(tfProvider),
		ujconfig.WithFeaturesPackage("internal/features"),
		ujconfig.WithDefaultResourceOptions(
			ExternalNameConfigurations(),
		),
		ujconfig.WithExampleManifestConfiguration(ujconfig.ExampleManifestConfiguration{
			ManagedResourceNamespace: "crossplane-system",
		}))

	Configure(pc)

	pc.ConfigureResources()
	return pc
}
