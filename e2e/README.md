# E2E Tests

The tests run against a **real ScyllaDB Cloud account** and create **billable
dedicated clusters** (AWS `t3.micro` x 3, `us-east-1`). They are not run in CI.

## Run

```bash
export SCYLLADB_CLOUD_TOKEN=<your API token>

make e2e        # Kind cluster + provider, uptest (e2e/manifests) and the default chainsaw tests
```

After `make e2e` the Kind cluster stays up, so you can re-run chainsaw tests:

```bash
make chainsaw-e2e                                                    # default tests
make chainsaw-e2e CHAINSAW_TESTS=e2e/tests/namespaced/allowlistrule  # one test
make chainsaw-e2e CHAINSAW_SELECTOR=suite=aws                        # AWS tests
make e2e-cleanup                                                     # leftovers of an interrupted run
```

### Standalone test (one chainsaw test, without the uptest run of `make e2e`)

```bash
make local-deploy                                              # Kind cluster + provider
make e2e-setup                                                 # credentials + provider configs
make chainsaw-e2e CHAINSAW_TESTS=e2e/tests/namespaced/cluster  # the test
kind delete cluster --name local-dev                           # when done
```

## GitHub Actions

The **E2E** workflow runs one test on demand (Actions → E2E → Run workflow):
pick the test and the branch, tag or commit to test. It reports the result as
an `e2e/<test>` commit status, so it shows up on that branch's pull request.
It needs the `SCYLLADB_CLOUD_TOKEN` repository secret, and the `aws` tests
need the variables below as repository variables.

## Tests

`e2e/tests/<cluster|namespaced>/<test>`. Each test creates its own cluster.

| Test | Suite | Needs |
|------|-------|-------|
| `cluster` | default | token |
| `allowlistrule` | default | token |
| `aws-connection` | aws | token + `SCYLLADB_E2E_TGW_*` |
| `aws-vpc-peering` | aws | token + `SCYLLADB_E2E_PEER_*` |
| `serverlesscluster` | serverless | token (free tier; serverless is a WIP in ScyllaDB Cloud) |

### AWS variables

| Variable | Value |
|----------|-------|
| `SCYLLADB_E2E_TGW_ID` | Transit Gateway ID in `us-east-1` |
| `SCYLLADB_E2E_TGW_RAM_ARN` | ARN of the RAM share that shares the Transit Gateway with ScyllaDB Cloud |
| `SCYLLADB_E2E_TGW_CIDRS` | comma-separated CIDRs to route through the attachment |
| `SCYLLADB_E2E_PEER_VPC_ID` | peer VPC ID |
| `SCYLLADB_E2E_PEER_ACCOUNT_ID` | AWS account ID of the peer VPC |
| `SCYLLADB_E2E_PEER_REGION` | region of the peer VPC |
| `SCYLLADB_E2E_PEER_CIDRS` | comma-separated CIDRs of the peer VPC (not overlapping `172.31.0.0/16`) |

The Transit Gateway attachment must be accepted (or auto-accepted) for
`aws-connection` to become Ready; the VPC peering doesn't need to be accepted.
