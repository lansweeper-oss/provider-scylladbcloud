package clients

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/upjet/v2/pkg/diffserver"
	"github.com/crossplane/upjet/v2/pkg/terraform"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// offlineToken is the token the offline setup configures the provider
	// with. It is never sent: offlineTransport refuses every request.
	offlineToken = "offline-diff-server-placeholder" //nolint:gosec // a placeholder, never sent

	errBuildOfflineClient = "cannot build the offline ScyllaDB Cloud client"

	// fmtErrOfflineRequestBlocked reports an API request that the offline diff
	// server refused to send. The query string is left out of the message.
	fmtErrOfflineRequestBlocked = "the offline diff server blocked an outbound ScyllaDB Cloud API request (%s %s): computing this diff requires calling the API, which is not possible offline"
)

// offlineClientFn builds the client the upstream resources expect as their
// meta, sending every request through the given RoundTripper.
type offlineClientFn func(rt http.RoundTripper) (any, error)

// OfflineTerraformSetupBuilder returns a Terraform setup for the diff server:
// it configures the provider without credentials and without contacting
// ScyllaDB Cloud. Use it in place of TerraformSetupBuilder in the diff server
// only.
//
// The ProviderConfig a resource references is still resolved, so that a
// resource whose ProviderConfig is missing fails the way it does in a
// reconcile, but its credentials are never read. The setup carries one client,
// built once for the process, that cannot send a request: see
// offlineTransport. Planning normally needs no request at all, because upjet's
// diff server observes from the state in the request instead of refreshing;
// the client is a safeguard for any code path that would call the API.
func OfflineTerraformSetupBuilder(newClient offlineClientFn) terraform.SetupFn {
	offlineClient := sync.OnceValues(func() (any, error) {
		c, err := newClient(offlineTransport{})
		if err != nil {
			return nil, fmt.Errorf(errBuildOfflineClient+": %w", err)
		}
		return c, nil
	})
	return func(ctx context.Context, c client.Client, mg resource.Managed) (terraform.Setup, error) {
		if _, err := resolveProviderConfig(ctx, c, mg); err != nil {
			return terraform.Setup{}, fmt.Errorf("cannot resolve provider config: %w", err)
		}
		meta, err := offlineClient()
		if err != nil {
			return terraform.Setup{}, err
		}
		return terraform.Setup{
			Configuration: map[string]any{keyToken: offlineToken},
			Meta:          meta,
		}, nil
	}
}

// offlineTransport refuses every HTTP request it sees, failing the diff at
// once with an error the diff server reports as DIFF_COMPUTATION_NOT_SUPPORTED,
// instead of letting the request reach ScyllaDB Cloud.
type offlineTransport struct{}

func (offlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, diffserver.NewDiffComputationNotSupportedError(fmt.Errorf(fmtErrOfflineRequestBlocked, req.Method, req.URL.Host+req.URL.Path))
}
