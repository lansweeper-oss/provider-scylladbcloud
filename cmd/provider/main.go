package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	changelogsv1alpha1 "github.com/crossplane/crossplane-runtime/v2/apis/changelogs/proto/v1alpha1"
	xpcontroller "github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/feature"
	"github.com/crossplane/crossplane-runtime/v2/pkg/gate"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/customresourcesgate"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/statemetrics"
	ujconfig "github.com/crossplane/upjet/v2/pkg/config"
	tjcontroller "github.com/crossplane/upjet/v2/pkg/controller"
	"github.com/crossplane/upjet/v2/pkg/diffserver"
	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	apisCluster "github.com/lansweeper-oss/provider-scylladbcloud/apis/cluster"
	apisNamespaced "github.com/lansweeper-oss/provider-scylladbcloud/apis/namespaced"
	"github.com/lansweeper-oss/provider-scylladbcloud/config"
	"github.com/lansweeper-oss/provider-scylladbcloud/internal/clients"
	controllerCluster "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/cluster"
	controllerNamespaced "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/namespaced"
	"github.com/lansweeper-oss/provider-scylladbcloud/internal/features"
	"github.com/lansweeper-oss/provider-scylladbcloud/internal/version"

	scyllaprovider "github.com/scylladb/terraform-provider-scylladbcloud/pkg/provider"
)

const (
	webhookTLSCertDirEnvVar = "WEBHOOK_TLS_CERT_DIR"
	tlsServerCertDirEnvVar  = "TLS_SERVER_CERTS_DIR"
	certsDirEnvVar          = "CERTS_DIR"
	tlsServerCertDir        = "/tls/server"

	// The diff-server command, as kong.Context.Command reports it.
	cmdDiffServer = "internal diff-server"
)

type certsDir string

func (d certsDir) BeforeApply(certsDirSet *bool) error {
	// we record whether the command-line option "--certs-dir" was supplied
	*certsDirSet = true
	return nil
}

// startCmd runs the provider's controllers. It is the default command, so the
// provider keeps behaving as before when it is invoked with flags alone.
type startCmd struct {
	LeaderElection bool `help:"Use leader election for the controller manager" short:"l" default:"false" env:"LEADER_ELECTION"`

	SyncPeriod              time.Duration `name:"sync" help:"Controller manager sync period such as 300ms, 1.5h, or 2h45m" short:"s" default:"1h"`
	PollInterval            time.Duration `name:"poll" help:"Poll interval controls how often an individual resource should be checked for drift." default:"10m"`
	PollStateMetricInterval time.Duration `name:"poll-state-metric" help:"State metric recording interval" default:"5s"`

	MaxReconcileRate         int    `help:"The global maximum rate per second at which resources may be checked for drift from the desired state." default:"10"`
	EnableSecretCache        bool   `help:"Cache Secrets in memory. Disabling it reads Secrets directly from the API server, which saves memory on clusters with many Secrets at the cost of more API requests." default:"true" env:"ENABLE_SECRET_CACHE"`
	EnableManagementPolicies bool   `help:"Enable support for Management Policies" default:"true" env:"ENABLE_MANAGEMENT_POLICIES"`
	EnableChangeLogs         bool   `name:"enable-changelogs" help:"Enable support for capturing change logs during reconciliation" default:"false" env:"ENABLE_CHANGE_LOGS"`
	ChangelogsSocketPath     string `help:"Path for changelogs socket (if enabled)" default:"/var/run/changelogs/changelogs.sock" env:"CHANGELOGS_SOCKET_PATH"`

	ClientCacheTTL time.Duration `name:"client-cache-ttl" help:"How long a configured ScyllaDB Cloud client, with its preloaded deployment metadata, is reused before it is rebuilt." default:"30m" env:"CLIENT_CACHE_TTL"`

	WebhookPort        int      `help:"The port the webhook listens on" default:"9443" env:"WEBHOOK_PORT"`
	MetricsBindAddress string   `help:"The address the metrics server listens on" default:":8080" env:"METRICS_BIND_ADDRESS"`
	CertsDir           certsDir `help:"The directory that contains the server key and certificate" default:"${defaultCertsDir}" env:"${defaultCertsDirEnvVar}"`
}

// diffServerCmd serves upjet's diff gRPC service, which the Crossplane CLI
// drives to simulate changes. It never contacts the ScyllaDB Cloud API or a
// cluster: plans are computed offline from the resources in each request.
type diffServerCmd struct {
	Network string `help:"The network the diff gRPC server listens on." default:"tcp" enum:"tcp,unix" env:"DIFF_SERVER_NETWORK"`
	Address string `help:"The address the diff gRPC server listens on. A file path when the network is unix." default:":9099" env:"DIFF_SERVER_ADDRESS"`
}

var cli struct {
	Debug bool `help:"Run with debug logging" short:"d"`

	Start    startCmd `cmd:"" default:"withargs" help:"Start the provider's controllers."`
	Internal struct {
		DiffServer diffServerCmd `cmd:"" name:"diff-server" help:"Start a gRPC server serving the provider's diff services. It never contacts the ScyllaDB Cloud API or a cluster: plans are computed offline from the resources in each request."`
	} `cmd:"" help:"Commands used internally by the Crossplane ecosystem. No compatibility guarantees are made for them."`
}

func main() {
	certsDirSet := false
	ctx := kong.Parse(&cli,
		kong.Description("Crossplane Provider for ScyllaDB Cloud"),
		kong.Bind(&certsDirSet),
		kong.Vars{
			"defaultCertsDir":       tlsServerCertDir,
			"defaultCertsDirEnvVar": certsDirEnvVar,
		},
	)

	log := newLogger(cli.Debug)

	// One upstream provider instance serves both scopes.
	tfProvider := scyllaprovider.New(context.Background())
	clusterProvider := config.GetProvider(tfProvider)
	namespacedProvider := config.GetProviderNamespaced(tfProvider)

	// kong only accepts the commands declared on cli, so anything other than the
	// diff server is the default start command.
	if ctx.Command() == cmdDiffServer {
		// Dispatched before a cluster connection (ctrl.GetConfig and the manager
		// below) is required: the diff server plans from the resources in each
		// request and never talks to a cluster or to the ScyllaDB Cloud API.
		ctx.FatalIfErrorf(serveDiffServer(ctrl.SetupSignalHandler(), log, cli.Internal.DiffServer.Network, cli.Internal.DiffServer.Address, clusterProvider, namespacedProvider), "Cannot run the diff gRPC server")
		return
	}

	ctx.FatalIfErrorf(validateClientCacheTTL(cli.Start.ClientCacheTTL), "Invalid --client-cache-ttl")

	// currently, we configure the jitter to be the 5% of the poll interval
	pollJitter := time.Duration(float64(cli.Start.PollInterval) * 0.05)

	log.Debug("Starting", "sync-period", cli.Start.SyncPeriod.String(), "poll-interval", cli.Start.PollInterval.String(), "max-reconcile-rate", cli.Start.MaxReconcileRate)

	cfg, err := ctrl.GetConfig()
	ctx.FatalIfErrorf(err, "Cannot get API server rest config")

	// Get the TLS certs directory from the environment variables set by
	// Crossplane if they're available.
	// In older XP versions we used WEBHOOK_TLS_CERT_DIR, in newer versions
	// we use TLS_SERVER_CERTS_DIR. If an explicit certs dir is not supplied
	// via the command-line options, then these environment variables are used
	// instead.
	if !certsDirSet {
		// backwards-compatibility concerns
		xpCertsDir := os.Getenv(certsDirEnvVar)
		if xpCertsDir == "" {
			xpCertsDir = os.Getenv(tlsServerCertDirEnvVar)
		}
		if xpCertsDir == "" {
			xpCertsDir = os.Getenv(webhookTLSCertDirEnvVar)
		}
		// we probably don't need this condition but just to be on the
		// safe side, if we are missing any kong machinery details...
		if xpCertsDir != "" {
			cli.Start.CertsDir = certsDir(xpCertsDir)
		}
	}

	scheme := runtime.NewScheme()
	ctx.FatalIfErrorf(clientgoscheme.AddToScheme(scheme), "Cannot add client-go APIs to scheme")
	ctx.FatalIfErrorf(apisCluster.AddToScheme(scheme), "Cannot add cluster-scoped provider-scylladbcloud APIs to scheme")
	ctx.FatalIfErrorf(apisNamespaced.AddToScheme(scheme), "Cannot add namespaced provider-scylladbcloud APIs to scheme")
	ctx.FatalIfErrorf(apiextensionsv1.AddToScheme(scheme), "Cannot add api-extensions APIs to scheme")

	var clientOpts client.Options
	if !cli.Start.EnableSecretCache {
		clientOpts.Cache = &client.CacheOptions{DisableFor: []client.Object{&corev1.Secret{}}}
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:           scheme,
		Client:           clientOpts,
		LeaderElection:   cli.Start.LeaderElection,
		LeaderElectionID: "crossplane-leader-election-provider-scylladbcloud",
		Cache: cache.Options{
			SyncPeriod:       &cli.Start.SyncPeriod,
			DefaultTransform: cache.TransformStripManagedFields(),
			// A ByObject transform replaces DefaultTransform for that type;
			// TransformStripCRDSchema strips managed fields as well.
			ByObject: map[client.Object]cache.ByObject{
				&apiextensionsv1.CustomResourceDefinition{}: {
					Transform: customresourcesgate.TransformStripCRDSchema,
				},
			},
		},
		Metrics: metricsserver.Options{
			BindAddress: cli.Start.MetricsBindAddress,
		},
		WebhookServer: webhook.NewServer(
			webhook.Options{
				CertDir: string(cli.Start.CertsDir),
				Port:    cli.Start.WebhookPort,
			}),
		LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
		LeaseDuration:              func() *time.Duration { d := 60 * time.Second; return &d }(),
		RenewDeadline:              func() *time.Duration { d := 50 * time.Second; return &d }(),
	})
	ctx.FatalIfErrorf(err, "Cannot create controller manager")

	metricRecorder := managed.NewMRMetricRecorder()
	stateMetrics := statemetrics.NewMRStateMetrics()

	metrics.Registry.MustRegister(metricRecorder)
	metrics.Registry.MustRegister(stateMetrics)

	// Both scopes share the client cache, so a credential set is only
	// configured once, and the operation store tracking in-flight async
	// Terraform operations.
	setupFn := clients.TerraformSetupBuilder(clients.NewClientCache(cli.Start.ClientCacheTTL))
	o := tjcontroller.NewOperationStore(log)

	// Both scopes share these options. In particular the global rate limiter
	// makes --max-reconcile-rate cap the provider, not each scope.
	opts := tjcontroller.Options{
		Logger:                  log,
		GlobalRateLimiter:       ratelimiter.NewGlobal(cli.Start.MaxReconcileRate),
		PollInterval:            cli.Start.PollInterval,
		MaxConcurrentReconciles: cli.Start.MaxReconcileRate,
		Features:                &feature.Flags{},
		MetricOptions: &xpcontroller.MetricOptions{
			PollStateMetricInterval: cli.Start.PollStateMetricInterval,
			MRMetrics:               metricRecorder,
			MRStateMetrics:          stateMetrics,
		},
		SetupFn:               setupFn,
		OperationTrackerStore: o,
		PollJitter:            pollJitter,
		StartWebhooks:         cli.Start.CertsDir != "",
	}

	if cli.Start.EnableManagementPolicies {
		opts.Features.Enable(features.EnableBetaManagementPolicies)
		log.Info("Beta feature enabled", "flag", features.EnableBetaManagementPolicies)
	}

	if cli.Start.EnableChangeLogs {
		opts.Features.Enable(feature.EnableAlphaChangeLogs)
		log.Info("Alpha feature enabled", "flag", feature.EnableAlphaChangeLogs)

		conn, err := grpc.NewClient("unix://"+cli.Start.ChangelogsSocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
		ctx.FatalIfErrorf(err, "failed to create change logs client connection at %s", cli.Start.ChangelogsSocketPath)

		opts.ChangeLogOptions = &xpcontroller.ChangeLogOptions{
			ChangeLogger: managed.NewGRPCChangeLogger(
				changelogsv1alpha1.NewChangeLogServiceClient(conn),
				managed.WithProviderVersion(fmt.Sprintf("provider-scylladbcloud:%s", version.Version))),
		}
	}

	clusterOpts, namespacedOpts := controllerOptions(opts, clusterProvider, namespacedProvider)

	canSafeStart, err := canWatchCRD(context.TODO(), mgr)
	ctx.FatalIfErrorf(err, "SafeStart precheck failed")
	if canSafeStart {
		crdGate := new(gate.Gate[schema.GroupVersionKind])
		clusterOpts.Gate = crdGate
		namespacedOpts.Gate = crdGate
		ctx.FatalIfErrorf(customresourcesgate.Setup(mgr, xpcontroller.Options{
			Logger:                  log,
			Gate:                    crdGate,
			MaxConcurrentReconciles: 1,
			Groups:                  providerGroups(scheme, clusterProvider.RootGroup, namespacedProvider.RootGroup),
		}), "Cannot setup CRD gate")
		log.Info("Registering gated controllers (controllers start when their CRDs become established)")
		ctx.FatalIfErrorf(controllerCluster.SetupGated(mgr, clusterOpts), "Cannot setup cluster-scoped provider-scylladbcloud controllers")
		ctx.FatalIfErrorf(controllerNamespaced.SetupGated(mgr, namespacedOpts), "Cannot setup namespaced provider-scylladbcloud controllers")
		log.Info("All gated controllers registered", "cluster-resources", len(clusterProvider.Resources), "namespaced-resources", len(namespacedProvider.Resources))
	} else {
		log.Info("Provider has missing RBAC permissions for watching CRDs, controller SafeStart capability will be disabled")
		ctx.FatalIfErrorf(controllerCluster.Setup(mgr, clusterOpts), "Cannot setup cluster-scoped provider-scylladbcloud controllers")
		ctx.FatalIfErrorf(controllerNamespaced.Setup(mgr, namespacedOpts), "Cannot setup namespaced provider-scylladbcloud controllers")
	}

	ctx.FatalIfErrorf(mgr.Start(ctrl.SetupSignalHandler()), "Cannot start controller manager")
}

// controllerOptions returns the controller options of each scope: the shared
// options with the scope's provider configuration.
func controllerOptions(shared tjcontroller.Options, cluster, namespaced *ujconfig.Provider) (clusterOpts, namespacedOpts tjcontroller.Options) {
	clusterOpts, namespacedOpts = shared, shared
	clusterOpts.Provider = cluster
	namespacedOpts.Provider = namespaced
	return clusterOpts, namespacedOpts
}

// validateClientCacheTTL rejects TTLs that would disable the client cache:
// every reconcile would then rebuild the client, which takes several API calls.
func validateClientCacheTTL(ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("client cache TTL must be positive, got %s", ttl)
	}
	return nil
}

// providerGroups returns every API group in the scheme that is one of the given
// root groups or a sub-group of one, so the CRD gate ignores unrelated CRDs.
func providerGroups(s *runtime.Scheme, roots ...string) []string {
	set := map[string]struct{}{}
	for gvk := range s.AllKnownTypes() {
		for _, root := range roots {
			if gvk.Group == root || strings.HasSuffix(gvk.Group, "."+root) {
				set[gvk.Group] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

func canWatchCRD(ctx context.Context, mgr manager.Manager) (bool, error) {
	if err := authv1.AddToScheme(mgr.GetScheme()); err != nil {
		return false, err
	}
	verbs := []string{"get", "list", "watch"}
	for _, verb := range verbs {
		sar := &authv1.SelfSubjectAccessReview{
			Spec: authv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authv1.ResourceAttributes{
					Group:    "apiextensions.k8s.io",
					Resource: "customresourcedefinitions",
					Verb:     verb,
				},
			},
		}
		if err := mgr.GetClient().Create(ctx, sar); err != nil {
			return false, errors.Wrapf(err, "unable to perform RBAC check for verb %s on CustomResourceDefinitions", verb)
		}
		if !sar.Status.Allowed {
			return false, nil
		}
	}
	return true, nil
}

// newLogger returns the provider's logger and sets controller-runtime's.
func newLogger(debug bool) logging.Logger {
	zl := zap.New(zap.UseDevMode(debug))
	if debug {
		// The controller-runtime runs with a no-op logger by default. It is
		// *very* verbose even at info level, so we only provide it a real
		// logger when we're running in debug mode.
		ctrl.SetLogger(zl)
	} else {
		// controller-runtime v0.23+ requires SetLogger to be called, otherwise
		// background goroutines (e.g. priority queue) will warn about missing logger.
		ctrl.SetLogger(logr.Discard())
	}
	return logging.NewLogrLogger(zl.WithName("provider-scylladbcloud"))
}

// serveDiffServer runs the diff gRPC server on the given network and address
// until the context is cancelled.
func serveDiffServer(ctx context.Context, log logging.Logger, network, address string, cluster, namespaced *ujconfig.Provider) error {
	s, scheme, err := newDiffServer(log, cluster, namespaced)
	if err != nil {
		return err
	}
	return s.Serve(ctx, network, address, scheme)
}

// newDiffServer returns the diff gRPC server, wired with both provider
// configurations to compute plans offline, and the runtime scheme it decodes
// plan requests with. The scheme holds the provider's own APIs and the
// Kubernetes core API, which is all a request can carry: the server never
// talks to a cluster.
func newDiffServer(log logging.Logger, cluster, namespaced *ujconfig.Provider) (*diffserver.Server, *runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, nil, errors.Wrap(err, "cannot add Kubernetes core APIs to the diff server scheme")
	}
	if err := apisCluster.AddToScheme(scheme); err != nil {
		return nil, nil, errors.Wrap(err, "cannot add cluster-scoped ScyllaDB Cloud APIs to the diff server scheme")
	}
	if err := apisNamespaced.AddToScheme(scheme); err != nil {
		return nil, nil, errors.Wrap(err, "cannot add namespaced ScyllaDB Cloud APIs to the diff server scheme")
	}
	s := diffserver.NewServer(
		diffserver.WithProviderConfigurations(cluster, namespaced),
		diffserver.WithLogger(log),
		diffserver.WithTerraformSetupFn(clients.OfflineTerraformSetupBuilder(scyllaprovider.NewOfflineClient)),
	)
	return s, scheme, nil
}
