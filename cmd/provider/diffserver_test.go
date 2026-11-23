package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	diffv1alpha1 "github.com/crossplane/upjet/v2/proto/diff/v1alpha1"
	scyllaprovider "github.com/scylladb/terraform-provider-scylladbcloud/pkg/provider"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/lansweeper-oss/provider-scylladbcloud/config"
)

// The manifests below are what a client puts in a plan request. Every
// ProviderConfig references a Secret that exists nowhere, which the offline
// diff server must never need.
const (
	clusterProviderConfig = `
apiVersion: scylladbcloud.m.crossplane.io/v1beta1
kind: ClusterProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: Secret
    secretRef:
      name: provider-secret
      namespace: crossplane-system
      key: credentials
`
	legacyProviderConfig = `
apiVersion: scylladbcloud.crossplane.io/v1beta1
kind: ProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: Secret
    secretRef:
      name: provider-secret
      namespace: crossplane-system
      key: credentials
`
	// A namespaced Cluster in a namespace of its own, referencing the
	// cluster-scoped ClusterProviderConfig above. Its plan runs the upstream
	// CustomizeDiff.
	namespacedCluster = `
apiVersion: scylladbcloud.scylladbcloud.m.crossplane.io/v1alpha1
kind: Cluster
metadata:
  name: example
  namespace: team-a
spec:
  forProvider:
    name: example
    cloud: AWS
    region: us-east-1
    nodeType: i3.xlarge
    minNodes: 3
  providerConfigRef:
    kind: ClusterProviderConfig
    name: default
`
	// The actual state of a namespaced Cluster as the provider records it after
	// a reconcile: the external name annotation and the observation the plan is
	// computed against.
	namespacedClusterActual = `
apiVersion: scylladbcloud.scylladbcloud.m.crossplane.io/v1alpha1
kind: Cluster
metadata:
  name: example
  namespace: team-a
  annotations:
    crossplane.io/external-name: "1234"
spec:
  forProvider:
    name: example
    cloud: AWS
    region: us-east-1
    nodeType: i3.xlarge
    minNodes: 3
  providerConfigRef:
    kind: ClusterProviderConfig
    name: default
status:
  atProvider:
    id: "1234"
    clusterId: 1234
    name: example
    cloud: AWS
    region: us-east-1
    nodeType: i3.xlarge
    minNodes: 3
    # Defaults and computed values that the upstream read records. They are all
    # ForceNew, so an observation without them would plan a replacement: a plan
    # is only as accurate as the observed state the request carries.
    alternatorWriteIsolation: only_rmw_uses_lwt
    enableDns: true
    enableVpcPeering: true
    userApiInterface: CQL
    cidrBlock: 172.31.0.0/16
    scyllaVersion: "2025.1.0"
    nodeDiskSize: 950
    availabilityZoneIds: [use1-az1, use1-az4, use1-az5]
    encryptionAtRest:
      - enabled: true
        keyId: key-1234
        provider: AWS
`
	namespacedClusterScaledOut = `
apiVersion: scylladbcloud.scylladbcloud.m.crossplane.io/v1alpha1
kind: Cluster
metadata:
  name: example
  namespace: team-a
spec:
  forProvider:
    name: example
    cloud: AWS
    region: us-east-1
    nodeType: i3.xlarge
    minNodes: 6
  providerConfigRef:
    kind: ClusterProviderConfig
    name: default
`
	namespacedClusterMoved = `
apiVersion: scylladbcloud.scylladbcloud.m.crossplane.io/v1alpha1
kind: Cluster
metadata:
  name: example
  namespace: team-a
spec:
  forProvider:
    name: example
    cloud: AWS
    region: us-west-2
    nodeType: i3.xlarge
    minNodes: 3
  providerConfigRef:
    kind: ClusterProviderConfig
    name: default
`
	// The actual state of a cluster-scoped AllowListRule.
	clusterAllowListRuleActual = `
apiVersion: scylladbcloud.scylladbcloud.crossplane.io/v1alpha1
kind: AllowListRule
metadata:
  name: example
  annotations:
    crossplane.io/external-name: "456"
spec:
  forProvider:
    clusterId: 1234
    cidrBlock: 203.0.113.10/32
  providerConfigRef:
    name: default
status:
  atProvider:
    id: "456"
    ruleId: 456
    clusterId: 1234
    cidrBlock: 203.0.113.10/32
`
	// cluster_id is updatable in place upstream.
	clusterAllowListRuleOtherCluster = `
apiVersion: scylladbcloud.scylladbcloud.crossplane.io/v1alpha1
kind: AllowListRule
metadata:
  name: example
spec:
  forProvider:
    clusterId: 5678
    cidrBlock: 203.0.113.10/32
  providerConfigRef:
    name: default
`
	// cidr_block is ForceNew upstream.
	clusterAllowListRuleOtherCIDR = `
apiVersion: scylladbcloud.scylladbcloud.crossplane.io/v1alpha1
kind: AllowListRule
metadata:
  name: example
spec:
  forProvider:
    clusterId: 1234
    cidrBlock: 203.0.113.20/32
  providerConfigRef:
    name: default
`
	missingProviderConfigCluster = `
apiVersion: scylladbcloud.scylladbcloud.m.crossplane.io/v1alpha1
kind: Cluster
metadata:
  name: example
  namespace: team-a
spec:
  forProvider:
    name: example
    region: us-east-1
  providerConfigRef:
    kind: ProviderConfig
    name: missing
`
)

// TestDiffServerPlan drives the diff gRPC server the way a client does, over a
// unix socket, wired exactly as main wires it. It covers a namespaced resource
// resolving a ClusterProviderConfig from its own namespace, every plan action,
// and a missing ProviderConfig.
func TestDiffServerPlan(t *testing.T) {
	ctx := context.Background()
	// A unix socket, because Serve doesn't report the port it bound when given
	// port zero. Its path is kept short: macOS limits socket paths to 104 bytes,
	// which the path t.TempDir returns can exceed.
	dir, err := os.MkdirTemp("", "diff")
	if err != nil {
		t.Fatalf("cannot create the socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "diff.sock")

	serveCtx, cancel := context.WithCancel(ctx)
	served := make(chan error, 1)
	tfProvider := scyllaprovider.New(ctx)
	go func() {
		served <- serveDiffServer(serveCtx, logging.NewNopLogger(), "unix", sock, config.GetProvider(tfProvider), config.GetProviderNamespaced(tfProvider))
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("the diff gRPC server failed: %v", err)
		}
	})

	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("cannot connect to the diff gRPC server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := diffv1alpha1.NewPlanServiceClient(conn)

	type want struct {
		action  diffv1alpha1.Action
		changes map[string]string
		code    codes.Code
		err     string
	}
	cases := map[string]struct {
		desired string
		actual  string
		store   []string
		want    want
	}{
		"NamespacedClusterCreateWithClusterProviderConfig": {
			desired: namespacedCluster,
			store:   []string{clusterProviderConfig},
			want: want{
				action: diffv1alpha1.Action_ACTION_CREATE,
				changes: map[string]string{
					"spec.forProvider.name":     "<unset> -> example",
					"spec.forProvider.cloud":    "<unset> -> AWS",
					"spec.forProvider.region":   "<unset> -> us-east-1",
					"spec.forProvider.nodeType": "<unset> -> i3.xlarge",
					"spec.forProvider.minNodes": "<unset> -> 3",
				},
			},
		},
		"NamespacedClusterScaleOutIsAnUpdate": {
			desired: namespacedClusterScaledOut,
			actual:  namespacedClusterActual,
			store:   []string{clusterProviderConfig},
			want: want{
				action:  diffv1alpha1.Action_ACTION_UPDATE,
				changes: map[string]string{"spec.forProvider.minNodes": "3 -> 6"},
			},
		},
		"NamespacedClusterRegionChangeIsAReplace": {
			desired: namespacedClusterMoved,
			actual:  namespacedClusterActual,
			store:   []string{clusterProviderConfig},
			want: want{
				action:  diffv1alpha1.Action_ACTION_REPLACE,
				changes: map[string]string{"spec.forProvider.region": "us-east-1 -> us-west-2 (requires replace)"},
			},
		},
		"ClusterAllowListRuleUpdate": {
			desired: clusterAllowListRuleOtherCluster,
			actual:  clusterAllowListRuleActual,
			store:   []string{legacyProviderConfig},
			want: want{
				action:  diffv1alpha1.Action_ACTION_UPDATE,
				changes: map[string]string{"spec.forProvider.clusterId": "1234 -> 5678"},
			},
		},
		"ClusterAllowListRuleReplace": {
			desired: clusterAllowListRuleOtherCIDR,
			actual:  clusterAllowListRuleActual,
			store:   []string{legacyProviderConfig},
			want: want{
				action:  diffv1alpha1.Action_ACTION_REPLACE,
				changes: map[string]string{"spec.forProvider.cidrBlock": "203.0.113.10/32 -> 203.0.113.20/32 (requires replace)"},
			},
		},
		"ClusterAllowListRuleNoOp": {
			desired: clusterAllowListRuleActual,
			actual:  clusterAllowListRuleActual,
			store:   []string{legacyProviderConfig},
			want:    want{action: diffv1alpha1.Action_ACTION_NO_OP},
		},
		"MissingProviderConfig": {
			desired: missingProviderConfigCluster,
			store:   []string{clusterProviderConfig},
			want:    want{code: codes.Internal, err: "cannot get referenced ProviderConfig"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := &diffv1alpha1.PlanRequest{
				DesiredResource: asStruct(t, tc.desired),
				ActualResource:  asStruct(t, tc.actual),
			}
			for _, m := range tc.store {
				req.KubernetesObjectStore = append(req.KubernetesObjectStore, asStruct(t, m))
			}

			callCtx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			rsp, err := client.Plan(callCtx, req, grpc.WaitForReady(true))
			if tc.want.err != "" {
				if err == nil {
					t.Fatalf("want the plan to fail with %q, got response: %s", tc.want.err, protojson.Format(rsp))
				}
				if st := status.Convert(err); st.Code() != tc.want.code || !strings.Contains(st.Message(), tc.want.err) {
					t.Fatalf("want a %s error containing %q, got %s: %s", tc.want.code, tc.want.err, st.Code(), st.Message())
				}
				return
			}
			if err != nil {
				t.Fatalf("cannot plan: %v", err)
			}
			if got := rsp.GetAction(); got != tc.want.action {
				t.Errorf("action: want %s, got %s\nfull response: %s", tc.want.action, got, protojson.Format(rsp))
			}
			if got := declaredChanges(rsp); !equalChanges(got, tc.want.changes) {
				t.Errorf("changes to the declared fields:\nwant %v\ngot  %v\nfull response: %s", tc.want.changes, got, protojson.Format(rsp))
			}
		})
	}
}

// collectionIndex matches the index of a collection element in a field path.
// A set element is indexed by the hash of its content, which no expectation
// should spell out, so indices are rendered as [*] instead.
var collectionIndex = regexp.MustCompile(`\[\d+\]`)

// declaredChanges renders the changes a plan reports for the fields the desired
// resource declares, as field to "actual -> planned". The changes the Terraform
// provider contributes on its own, such as computed values on a create, are
// left out, so that an upstream bump changing a default doesn't fail here.
func declaredChanges(rsp *diffv1alpha1.PlanResponse) map[string]string {
	changes := map[string]string{}
	for _, c := range rsp.GetChanges() {
		if c.GetOrigin() != diffv1alpha1.Origin_ORIGIN_DESIRED_STATE {
			continue
		}
		change := renderValue(c.GetActual()) + " -> " + renderValue(c.GetPlanned())
		if c.GetRequiresReplace() {
			change += " (requires replace)"
		}
		changes[collectionIndex.ReplaceAllString(c.GetField(), "[*]")] = change
	}
	return changes
}

func equalChanges(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func renderValue(v *diffv1alpha1.FieldValue) string {
	switch {
	case v == nil:
		return "<unset>"
	case v.GetValue() != nil:
		return fmt.Sprintf("%v", v.GetValue().AsInterface())
	default:
		return v.GetAbsence().String()
	}
}

// asStruct converts a YAML manifest into the protobuf Struct a plan request
// carries it as. An empty manifest converts to nil, which is how a request says
// that the resource doesn't exist yet.
func asStruct(t *testing.T, manifest string) *structpb.Struct {
	t.Helper()
	if manifest == "" {
		return nil
	}
	j, err := yaml.ToJSON([]byte(manifest))
	if err != nil {
		t.Fatalf("cannot convert the manifest to JSON: %v", err)
	}
	s := &structpb.Struct{}
	if err := s.UnmarshalJSON(j); err != nil {
		t.Fatalf("cannot convert the manifest to a protobuf Struct: %v", err)
	}
	return s
}
