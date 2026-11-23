package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	tfsdk "github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/scylladb/terraform-provider-scylladbcloud/pkg/provider"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/upjet/v2/pkg/terraform"

	clusterv1beta1 "github.com/lansweeper-oss/provider-scylladbcloud/apis/cluster/v1beta1"
	namespacedv1beta1 "github.com/lansweeper-oss/provider-scylladbcloud/apis/namespaced/v1beta1"
)

const (
	// error messages
	errNoProviderConfig     = "no providerConfigRef provided"
	errGetProviderConfig    = "cannot get referenced ProviderConfig"
	errTrackUsage           = "cannot track ProviderConfig usage"
	errExtractCredentials   = "cannot extract credentials"
	errUnmarshalCredentials = "cannot unmarshal scylladbcloud credentials as JSON"
	errNoToken              = "credentials must contain a non-empty \"token\""
	errConfigureClient      = "cannot configure ScyllaDB Cloud client"
)

// Keys of the JSON credentials secret, named after the Terraform provider
// arguments they map to.
const (
	keyToken    = "token"
	keyEndpoint = "endpoint"
)

// TerraformSetupBuilder returns a terraform.SetupFn that resolves the
// ProviderConfig credentials and hands the in-process Terraform provider a
// configured ScyllaDB Cloud client as its meta.
func TerraformSetupBuilder(clientCache *ClientCache) terraform.SetupFn {
	return func(ctx context.Context, client client.Client, mg resource.Managed) (terraform.Setup, error) {
		pcSpec, err := resolveProviderConfig(ctx, client, mg)
		if err != nil {
			return terraform.Setup{}, fmt.Errorf("cannot resolve provider config: %w", err)
		}

		data, err := resource.CommonCredentialExtractor(ctx, pcSpec.Credentials.Source, client, pcSpec.Credentials.CommonCredentialSelectors)
		if err != nil {
			return terraform.Setup{}, fmt.Errorf(errExtractCredentials+": %w", err)
		}
		creds := map[string]string{}
		if err := json.Unmarshal(data, &creds); err != nil {
			return terraform.Setup{}, fmt.Errorf(errUnmarshalCredentials+": %w", err)
		}

		cfg, err := providerConfiguration(creds)
		if err != nil {
			return terraform.Setup{}, err
		}

		// Upjet passes Meta straight to the SDKv2 resource functions and never
		// runs the provider's configure step itself, so the client is built here.
		meta, err := clientCache.Get(ctx, cfg)
		if err != nil {
			return terraform.Setup{}, fmt.Errorf(errConfigureClient+": %w", err)
		}

		return terraform.Setup{Configuration: cfg, Meta: meta}, nil
	}
}

// providerConfiguration maps the credentials secret to the Terraform provider
// configuration. The upstream provider does not validate its configuration when
// configured in-process, so the required token is checked here.
func providerConfiguration(creds map[string]string) (map[string]any, error) {
	// Secrets created with `echo` carry a trailing newline, which would end
	// up in the Authorization header.
	token := strings.TrimSpace(creds[keyToken])
	if token == "" {
		return nil, errors.New(errNoToken)
	}
	cfg := map[string]any{keyToken: token}
	if endpoint := strings.TrimSpace(creds[keyEndpoint]); endpoint != "" {
		cfg[keyEndpoint] = endpoint
	}
	return cfg, nil
}

// configureScyllaClient runs the upstream provider's own configure logic, so
// the client is built exactly as Terraform would build it. A fresh provider is
// used every time because configuring stores the client on the instance.
func configureScyllaClient(ctx context.Context, cfg map[string]any) (any, error) {
	p := provider.New(ctx)
	if diags := p.Configure(ctx, tfsdk.NewResourceConfigRaw(cfg)); diags.HasError() {
		return nil, diagnosticsError(diags)
	}
	return p.Meta(), nil
}

// diagnosticsError joins the error diagnostics into a single error.
func diagnosticsError(diags diag.Diagnostics) error {
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		if d.Severity != diag.Error {
			continue
		}
		msg := d.Summary
		if d.Detail != "" {
			msg += ": " + d.Detail
		}
		msgs = append(msgs, msg)
	}
	return errors.New(strings.Join(msgs, "; "))
}

func toSharedPCSpec(pc *clusterv1beta1.ProviderConfig) (*namespacedv1beta1.ProviderConfigSpec, error) {
	if pc == nil {
		return nil, nil
	}
	data, err := json.Marshal(pc.Spec)
	if err != nil {
		return nil, err
	}

	var mSpec namespacedv1beta1.ProviderConfigSpec
	err = json.Unmarshal(data, &mSpec)
	return &mSpec, err
}

func resolveProviderConfig(ctx context.Context, crClient client.Client, mg resource.Managed) (*namespacedv1beta1.ProviderConfigSpec, error) {
	switch managed := mg.(type) {
	case resource.LegacyManaged: //nolint:staticcheck // still handling cluster-scoped behavior
		return resolveLegacy(ctx, crClient, managed)
	case resource.ModernManaged:
		return resolveModern(ctx, crClient, managed)
	default:
		return nil, errors.New("resource is not a managed resource")
	}
}

func resolveLegacy(ctx context.Context, client client.Client, mg resource.LegacyManaged) (*namespacedv1beta1.ProviderConfigSpec, error) { //nolint:staticcheck // still handling cluster-scoped behavior
	configRef := mg.GetProviderConfigReference()
	if configRef == nil {
		return nil, errors.New(errNoProviderConfig)
	}
	pc := &clusterv1beta1.ProviderConfig{}
	if err := client.Get(ctx, types.NamespacedName{Name: configRef.Name}, pc); err != nil {
		return nil, fmt.Errorf(errGetProviderConfig+": %w", err)
	}

	t := resource.NewLegacyProviderConfigUsageTracker(client, &clusterv1beta1.ProviderConfigUsage{})
	if err := t.Track(ctx, mg); err != nil {
		return nil, fmt.Errorf(errTrackUsage+": %w", err)
	}

	return toSharedPCSpec(pc)
}

func resolveModern(ctx context.Context, crClient client.Client, mg resource.ModernManaged) (*namespacedv1beta1.ProviderConfigSpec, error) {
	configRef := mg.GetProviderConfigReference()
	if configRef == nil {
		return nil, errors.New(errNoProviderConfig)
	}

	pcRuntimeObj, err := crClient.Scheme().New(namespacedv1beta1.SchemeGroupVersion.WithKind(configRef.Kind))
	if err != nil {
		return nil, fmt.Errorf("unknown GVK for ProviderConfig: %w", err)
	}
	pcObj, ok := pcRuntimeObj.(client.Object)
	if !ok {
		// This indicates a programming error, types are not properly generated
		return nil, errors.New(" is not an Object")
	}

	// A ClusterProviderConfig is cluster-scoped. The API server ignores a
	// namespace on its lookup, but the diff server reads it from an in-memory
	// store keyed on the exact namespace, so it must be cleared here rather than
	// left to the client.
	ns := mg.GetNamespace()
	if configRef.Kind == namespacedv1beta1.ClusterProviderConfigKind {
		ns = ""
	}
	if err := crClient.Get(ctx, types.NamespacedName{Name: configRef.Name, Namespace: ns}, pcObj); err != nil {
		return nil, fmt.Errorf(errGetProviderConfig+": %w", err)
	}

	var pcSpec namespacedv1beta1.ProviderConfigSpec
	pcu := &namespacedv1beta1.ProviderConfigUsage{}
	switch pc := pcObj.(type) {
	case *namespacedv1beta1.ProviderConfig:
		pcSpec = pc.Spec
		if pcSpec.Credentials.SecretRef != nil {
			pcSpec.Credentials.SecretRef.Namespace = mg.GetNamespace()
		}
	case *namespacedv1beta1.ClusterProviderConfig:
		pcSpec = pc.Spec
	default:
		return nil, errors.New("unknown provider config type")
	}
	t := resource.NewProviderConfigUsageTracker(crClient, pcu)
	if err := t.Track(ctx, mg); err != nil {
		return nil, fmt.Errorf(errTrackUsage+": %w", err)
	}
	return &pcSpec, nil
}
