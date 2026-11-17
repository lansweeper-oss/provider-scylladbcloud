# Provider ScyllaDB Cloud

`provider-scylladbcloud` is a [Crossplane](https://crossplane.io/) provider that
manages [ScyllaDB Cloud](https://cloud.docs.scylladb.com/) resources from
Kubernetes. Built with [Upjet](https://github.com/crossplane/upjet) code
generation tools, it runs the upstream Terraform provider in-process and exposes
XRM-conformant managed resources for clusters, serverless clusters, cluster
connections, allowlist rules and VPC peerings.

[![Upstream Terraform Provider](https://img.shields.io/badge/upstream_terraform_provider-v1.13.0-blue?logo=terraform)](https://github.com/scylladb/terraform-provider-scylladbcloud/tree/v1.13.0)

## Getting Started

### Prerequisites

- A Kubernetes cluster with [Crossplane](https://crossplane.io/) installed
- A ScyllaDB Cloud account and an API token

### Authentication

Create a Kubernetes secret containing your ScyllaDB Cloud credentials as JSON.
`token` is required; `endpoint` is optional and defaults to
`https://api.cloud.scylladb.com`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: scylladbcloud-creds
  namespace: crossplane-system
type: Opaque
stringData:
  credentials: |
    {
      "token": "your-api-token",
      "endpoint": "https://api.cloud.scylladb.com"
    }
```

Then reference it in a `ProviderConfig`:

```yaml
apiVersion: scylladbcloud.crossplane.io/v1beta1
kind: ProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: Secret
    secretRef:
      name: scylladbcloud-creds
      namespace: crossplane-system
      key: credentials
```

### Managed resources

All resources are in the `scylladbcloud.scylladbcloud.crossplane.io` group
(cluster-scoped) and `scylladbcloud.scylladbcloud.m.crossplane.io`
(namespaced):

| Kind | Terraform resource |
|------|--------------------|
| `Cluster` | `scylladbcloud_cluster` |
| `ServerlessCluster` | `scylladbcloud_serverless_cluster` |
| `ClusterConnection` | `scylladbcloud_cluster_connection` |
| `AllowListRule` | `scylladbcloud_allowlist_rule` |
| `VpcPeering` | `scylladbcloud_vpc_peering` |

### Simulating changes

The provider image also serves upjet's diff gRPC service (`provider internal
diff-server`, declared as the `DiffServer` package capability), which the
Crossplane CLI's simulate commands use to show what applying a manifest would
change (create, update, replace or no-op). Plans are computed offline: the
server never reads credentials, never contacts a cluster and refuses every
request to the ScyllaDB Cloud API.

### Client cache

Configuring a ScyllaDB Cloud client preloads deployment metadata (Scylla
versions, cloud providers and regions) from the API. The provider keeps one
client per set of credentials and rebuilds it after `--client-cache-ttl`
(`CLIENT_CACHE_TTL`, default `30m`).

## Developing

Run code-generation pipeline:

```console
make generate
```

Run against a Kubernetes cluster (out of cluster):

```console
make run
```

or (deploying in-cluster):

```console
make local-deploy
```

Run e2e tests (locally in a KinD cluster, against a real ScyllaDB Cloud
account; see [e2e/README.md](e2e/README.md) for cost and the opt-in suites):

```console
export SCYLLADB_CLOUD_TOKEN=<your API token>
make e2e
```

Build, push, and install:

```console
make all
```

Build binary:

```console
make build
```

## Contributing

Refer to Crossplane's [CONTRIBUTING.md] file for more information on how the
Crossplane community prefers to work. The [Provider Development][provider-dev]
guide may also be of use.

## Report a Bug

For filing bugs, suggesting improvements, or requesting new features, please
open an [issue].

## Code of conduct

This project follows the [CNCF Code of Conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md).

It also adheres to the [Crossplane AI Policy](https://github.com/crossplane/crossplane/blob/main/AI_POLICY.md).

## Licensing

`provider-scylladbcloud` is under the Apache 2.0 [license](LICENSE).

<!-- Links -->

[CONTRIBUTING.md]: https://github.com/crossplane/crossplane/blob/main/contributing
[issue]: https://github.com/lansweeper-oss/provider-scylladbcloud/issues
[provider-dev]: https://github.com/crossplane/crossplane/blob/master/contributing/guide-provider-development.md
