//go:build providershim

// Package provider exposes the upstream ScyllaDB Cloud Terraform provider's
// internal packages to the Crossplane provider. It is copied into the upstream
// module tree (third_party/terraform-provider-scylladbcloud/pkg/provider) by
// `make provider-source`: only code inside that module may import internal/.
// Its build constraint keeps it out of this module's build, and the copy drops it.
// It isn't "ignore": gopls type-checks those files standalone and reports the internal/ imports.
package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	internalprovider "github.com/scylladb/terraform-provider-scylladbcloud/internal/provider"
	"github.com/scylladb/terraform-provider-scylladbcloud/internal/scylla"
)

// Placeholders the offline client is built with. They are never sent anywhere:
// every request goes through the RoundTripper given to NewOfflineClient. The
// endpoint uses the reserved .invalid TLD as a second line of defence.
const (
	offlineEndpoint  = "https://scylladbcloud.offline.invalid"
	offlineToken     = "offline-diff-server-placeholder" //nolint:gosec // a placeholder, never sent
	offlineUserAgent = "crossplane-provider-scylladbcloud-offline-diff-server"
)

// New exposes the upstream internal SDKv2 provider constructor to other modules,
// with the cluster resource extended by AddCQLCredentials.
func New(ctx context.Context) *schema.Provider {
	p := internalprovider.New(ctx)
	AddCQLCredentials(p.ResourcesMap["scylladbcloud_cluster"])
	return p
}

// AddCQLCredentials adds the cql_username and cql_password attributes to the cluster resource.
// They hold what the scylladbcloud_cql_auth data source returns, which Upjet can't expose
// because it has no data source support. The password is sensitive, so Upjet writes it
// to the managed resource's connection secret and leaves it out of the status.
func AddCQLCredentials(r *schema.Resource) {
	r.Schema["cql_username"] = &schema.Schema{
		Description: "The username of the cluster's default CQL user.",
		Type:        schema.TypeString,
		Computed:    true,
	}
	r.Schema["cql_password"] = &schema.Schema{
		Description: "The password of the cluster's default CQL user.",
		Type:        schema.TypeString,
		Computed:    true,
		Sensitive:   true,
	}
	r.CreateContext = withCQLCredentials(r.CreateContext)
	r.ReadContext = withCQLCredentials(r.ReadContext)
}

type resourceOp = func(context.Context, *schema.ResourceData, any) diag.Diagnostics

// withCQLCredentials runs op, then fetches the CQL credentials of the cluster op left in d.
// A failed fetch is only a warning, like a failed CA certificate fetch upstream:
// it must never break a read or leak a cluster that was just created. The next read retries.
//
// shortcut: credentials are fetched only while unset, which saves an API call per reconcile
// but misses a password changed outside Terraform; fetch on every read if that's ever needed.
func withCQLCredentials(op resourceOp) resourceOp {
	return func(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
		diags := op(ctx, d, meta)
		clusterID := int64(d.Get("cluster_id").(int))
		if diags.HasError() || d.Id() == "" || clusterID == 0 || d.Get("cql_password").(string) != "" {
			return diags
		}
		conn, err := meta.(*scylla.Client).Connect(ctx, clusterID)
		if err != nil {
			return append(diags, diag.Diagnostic{
				Severity: diag.Warning,
				Summary:  fmt.Sprintf("Unable to retrieve CQL credentials for cluster %d", clusterID),
				Detail:   err.Error(),
			})
		}
		_ = d.Set("cql_username", conn.Credentials.Username)
		_ = d.Set("cql_password", conn.Credentials.Password)
		return diags
	}
}

// NewOfflineClient returns the client the provider's resources expect as their
// meta, built without contacting ScyllaDB Cloud, for the Crossplane provider's
// offline diff server:
//   - no deployment metadata is preloaded (that would call the API), and Meta is
//     an empty Cloudmeta so that a lookup fails with an error, not a nil panic;
//   - every request of the client is sent through rt instead of the network;
//   - the V2 client, which builds its own unexported http.Client, is left nil.
//     Only the scylladbcloud_stack resource uses it, and the Crossplane provider
//     doesn't expose that resource.
func NewOfflineClient(rt http.RoundTripper) (any, error) {
	c, err := scylla.NewClient(offlineEndpoint, offlineToken, offlineUserAgent, "", false)
	if err != nil {
		return nil, err
	}
	c.HTTPClient.Transport = rt
	c.Meta = &scylla.Cloudmeta{}
	c.V2 = nil
	return c, nil
}
