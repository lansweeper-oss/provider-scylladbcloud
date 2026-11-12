package config

import (
	"context"
	// Note(turkenh): we are importing this to embed provider schema document
	_ "embed"

	ujconfig "github.com/crossplane/upjet/v2/pkg/config"

	"github.com/scylladb/terraform-provider-scylladbcloud/pkg/provider"
)

const (
	resourcePrefix = "scylladbcloud"
	modulePath     = "github.com/lansweeper-oss/provider-scylladbcloud"
)

//go:embed schema.json
var providerSchema string

//go:embed provider-metadata.yaml
var providerMetadata string

// GetProvider returns provider configuration
func GetProvider() *ujconfig.Provider {
	pc := ujconfig.NewProvider([]byte(providerSchema), resourcePrefix, modulePath, []byte(providerMetadata),
		ujconfig.WithRootGroup("scylladbcloud.crossplane.io"),
		ujconfig.WithIncludeList(nil),
		ujconfig.WithTerraformPluginSDKIncludeList(ExternalNameConfigured()),
		ujconfig.WithTerraformProvider(provider.New(context.Background())),
		ujconfig.WithFeaturesPackage("internal/features"),
		ujconfig.WithDefaultResourceOptions(
			ExternalNameConfigurations(),
		))

	for _, configure := range []func(provider *ujconfig.Provider){
		// add custom config functions
	} {
		configure(pc)
	}

	pc.ConfigureResources()
	return pc
}

// GetProviderNamespaced returns the namespaced provider configuration
func GetProviderNamespaced() *ujconfig.Provider {
	pc := ujconfig.NewProvider([]byte(providerSchema), resourcePrefix, modulePath, []byte(providerMetadata),
		ujconfig.WithRootGroup("scylladbcloud.m.crossplane.io"),
		ujconfig.WithIncludeList(nil),
		ujconfig.WithTerraformPluginSDKIncludeList(ExternalNameConfigured()),
		ujconfig.WithTerraformProvider(provider.New(context.Background())),
		ujconfig.WithFeaturesPackage("internal/features"),
		ujconfig.WithDefaultResourceOptions(
			ExternalNameConfigurations(),
		),
		ujconfig.WithExampleManifestConfiguration(ujconfig.ExampleManifestConfiguration{
			ManagedResourceNamespace: "crossplane-system",
		}))

	for _, configure := range []func(provider *ujconfig.Provider){
		// add custom config functions
	} {
		configure(pc)
	}

	pc.ConfigureResources()
	return pc
}
