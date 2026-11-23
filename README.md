# Crusoe Registry Token Issuer

Simple Go HTTP service that mints short-lived [Crusoe Container Registry][ccr] (CCR) tokens on request.
It is meant to sit behind an [External Secrets Operator][eso] (ESO) webhook generator,
which turns each token into a `kubernetes.io/dockerconfigjson` secret.

## Background

Crusoe Managed Kubernetes (CMK) nodes have no keyless way to pull from CCR.
The only supported option is a registry token in an `imagePullSecret`.

Crusoe ships the [Crusoe Registry Token Rotator][rotator] for this.
It's a Helm chart which consists of a Python script that mints tokens on a schedule,
and writes the pull secret into every namespace listed in its values.
For our purposes, it fell short in a few places:

- **The namespace list is static.** Every new namespace meant upgrading the chart and triggering a `Job` run.
- **It writes `Secret` resources itself**, so it needs RBAC to create and update them in every target namespace.
  With many namespaces, many resources are created, slowing down the rollout.
- **The registry username is set by hand.** Crusoe currently has no service accounts,
  so the username has to match the user who owns the API key pair.

This service only mints tokens. ESO does the rest:
a `ClusterGenerator` of kind `Webhook` sends a `POST` request to the token issuer,
and a `ClusterExternalSecret` writes the result into every `Namespace` that matches a label selector.
Namespaces are enrolled automatically when they carry the label.
The issuer has no Kubernetes API access, and the registry username comes directly from the Crusoe API.

> [!NOTE]
> We consider this an interim solution. The webhook generator is stateless,
> so ESO can't ask the issuer to revoke a token it has replaced.
> Old tokens stay valid until they expire.
> An ideal solution would be a native Crusoe generator in ESO with `GeneratorState` and `Cleanup`,
> which would revoke tokens on deletion and/or rotation.
> Once that generator exists, this service will no longer be needed.

## What it does

On startup, the issuer fetches the identity behind the access and secret key pair.
If that call fails, it exits immediately with status 1.
Otherwise, it serves the following routes, relative to `CRUSOE_ISSUER_SERVER_BASE_PATH`:

| Method | Path            | Description                                                                         |
|--------|-----------------|-------------------------------------------------------------------------------------|
| `POST` | `/token`        | Mints a new registry token and returns it with the username and registry host.      |
| `GET`  | `/healthz`      | Liveness. Always `200` with `{"status":"up"}`.              |
| `GET`  | `/readyz`       | Readiness. Checks the Crusoe API every 5 minutes, `503` after 2 failures in a row.  |
| `GET`  | `/debug/var`    | `expvar` output. `404` unless debug mode is enabled.                                |
| `GET`  | `/debug/pprof/*` | `pprof` profiles. `404` unless debug mode is enabled.                               |

A successful `POST /token` returns `200` with `Cache-Control: no-store`:

```json
{
  "username": "john.doe@example.com",
  "password": "...",
  "registry": "registry.*.ccr.crusoecloudcompute.com"
}
```

Errors come back as `{"error": "..."}`:

| Status | When                                                                 |
|--------|----------------------------------------------------------------------|
| `408`  | The request took longer than `CRUSOE_ISSUER_SERVER_TIMEOUT_REQUEST`. |
| `429`  | The Crusoe API rate-limited us and retries ran out.                  |
| `502`  | Any other failure talking to the Crusoe API.                         |

Each token gets the alias `<token-prefix><expiry>-<random>`,
with the expiry in UTC as `YYYYMMDDhhmmss`, for example `ccr-token-20261124120000-<random>`,
where `<token-prefix>` is the value of the `CRUSOE_ISSUER_TOKEN_PREFIX` environment variable.
The prefix lets you easily identify tokens in the Crusoe console.

### Caveats

- **Old tokens are not revoked.** The Crusoe CLI can delete CCR tokens with `crusoe registry tokens delete`,
  but that endpoint is missing from the Crusoe OpenAPI spec and `client-go`, so the issuer doesn't call it.
  Tokens expire after `CRUSOE_ISSUER_TOKEN_TTL`.
- **`CRUSOE_ISSUER_TOKEN_TTL` must be longer than the ESO `refreshInterval`.**
  ESO keeps the last `Secret` when a refresh fails, so the gap between the two
  is how long the issuer can be down before pulls start failing.
- **The issuer has no authentication.** Anything that can reach the server can mint a token.
  Bind to `127.0.0.1` when running as a sidecar, or lock the port down with a `NetworkPolicy`.
- **Every call mints a new token.** A `ClusterExternalSecret` creates an `ExternalSecret` per `Namespace`,
  and each calls the generator on its own.
- **The ESO webhook `timeout` must be longer than `CRUSOE_ISSUER_SERVER_TIMEOUT_REQUEST` (30s).**
  Otherwise, ESO gives up first and the token minted for that request is orphaned.
  Upstream calls retry up to 4 times on transport errors and `429`, with 1, 2, 4 and 8 seconds of backoff,
  which uses up half of those 30 seconds.

## Configuration

Configuration is environment-only. Every variable carries the `CRUSOE_` prefix.
Durations use Go syntax (`720m`, `45s`, `1m30s`) and must be positive.

| Variable                                | Type                                   | Default                                 | Description                                                             |
|-----------------------------------------|----------------------------------------|-----------------------------------------|-------------------------------------------------------------------------|
| `CRUSOE_ACCESS_KEY`                     | `string`                               |                                         | Crusoe API access key.                                                  |
| `CRUSOE_SECRET_KEY`                     | `string`                               |                                         | Crusoe API secret key.                                                  |
| `CRUSOE_ISSUER_REGISTRY`                | `string`                               | `registry.*.ccr.crusoecloudcompute.com` | Registry host returned in the `registry` field.                         |
| `CRUSOE_ISSUER_SERVER_DEBUG`            | `bool`                                 | `false`                                 | Gin debug mode, request header logging, and the `/debug` routes.        |
| `CRUSOE_ISSUER_SERVER_HOST`             | `string`                               | `""`                                    | Listen address. Empty listens on every interface.                       |
| `CRUSOE_ISSUER_SERVER_PORT`             | `uint16`                               | `5001`                                  | Listen port.                                                            |
| `CRUSOE_ISSUER_SERVER_BASE_PATH`        | `string`                               | `/api/v1`                               | Prefix for every route.                                                 |
| `CRUSOE_ISSUER_SERVER_TIMEOUT_REQUEST`  | `time.Duration`                        | `30s`                                   | Deadline per request. Also applies to the startup and readiness checks. |
| `CRUSOE_ISSUER_SERVER_TIMEOUT_SHUTDOWN` | `time.Duration`                        | `25s`                                   | How long in-flight requests get to finish after `SIGTERM`.              |
| `CRUSOE_ISSUER_TOKEN_TTL`               | `time.Duration`                        | `12h`                                   | Lifetime of each minted token.                                          |
| `CRUSOE_ISSUER_TOKEN_PREFIX`            | `string`                               | `ccr-token-`                            | Prefix of each token alias.                                             |
| `CRUSOE_ISSUER_LOG_FORMAT`              | `json` \| `text`                       | `json`                                  | Log handler.                                                            |
| `CRUSOE_ISSUER_LOG_LEVEL`               | `debug` \| `info` \| `warn` \| `error` | `info`                                  | Minimum log level.                                                      |
| `CRUSOE_ISSUER_LOG_SOURCE`              | `bool`                                 | `false`                                 | Adds the source file and line to each log record.                       |

## Example usage with ESO

Assuming that `ClusterGenerator` reconciliation is enabled and the issuer runs as a sidecar in the ESO controller:

```yaml
apiVersion: generators.external-secrets.io/v1alpha1
kind: ClusterGenerator
metadata:
  name: crusoe-registry-token
spec:
  kind: Webhook
  generator:
    webhookSpec:
      url: http://localhost:5001/api/v1/token
      method: POST
      timeout: 45s
      result:
        jsonPath: "$"
```

The `ClusterExternalSecret` fans it out to every labeled namespace:

```yaml
apiVersion: external-secrets.io/v1
kind: ClusterExternalSecret
metadata:
  name: crusoe-regcred-ro
spec:
  externalSecretName: crusoe-regcred-ro
  namespaceSelectors:
    - matchLabels:
        external-secrets.crusoe.ai/managed: "true"
  externalSecretSpec:
    refreshInterval: 6h
    target:
      name: crusoe-regcred-ro
      creationPolicy: Owner
      template:
        type: kubernetes.io/dockerconfigjson
        data:
          .dockerconfigjson: |
            {"auths":{"{{ .registry }}":{"username":"{{ .username }}","password":"{{ .password }}","auth":"{{ printf "%s:%s" .username .password | b64enc }}"}}}
    dataFrom:
      - sourceRef:
          generatorRef:
            apiVersion: generators.external-secrets.io/v1alpha1
            kind: ClusterGenerator
            name: crusoe-registry-token
```

## Local development

Prerequisites: Go 1.27 or later, [`just`](https://just.systems) 1.55 or later,
and [`pre-commit`](https://pre-commit.com), which is required.
[`golangci-lint`](https://golangci-lint.run) v2 is needed for `just lint` and `just format`,
as the `pre-commit` hooks build their own copy. Docker only if you work with the image,
`helm` and `kubectl` only if you work with the chart.

```sh
git clone https://github.com/jetbrains-research/crusoe-registry-token-issuer
cd crusoe-registry-token-issuer
just setup
```

Run `just` on its own to list every recipe.

Local runs read from a `.env` file you create yourself:

```dotenv
CRUSOE_ACCESS_KEY=...
CRUSOE_SECRET_KEY=...

CRUSOE_ISSUER_SERVER_HOST=127.0.0.1
CRUSOE_ISSUER_LOG_FORMAT=text
CRUSOE_ISSUER_LOG_LEVEL=debug
CRUSOE_ISSUER_TOKEN_TTL=1h
```

Run it:

```sh
just run # append `true` to enable debug mode
```

This recipe will load `.env` before running the server.
`http/api.http` has requests for every route, for the JetBrains HTTP client.

The `pre-commit` hooks format the code and lint it with [`golangci-lint`](https://golangci-lint.run),
using the configuration in `.golangci.yaml`. To run them against the whole repository:

```sh
just hook
```

Checks before pushing:

```sh
just lint
just format
just vet
just test
just lock
```

`just lint` and `just format` rewrite files in place.
`just test` takes an optional package pattern and extra `go test` flags,
for example `just test ./internal/config/... -v`.

Dependencies:

```sh
just upgrade
just tidy
```

### Building

```sh
just binary # writes bin/crusoe-registry-token-issuer
just build  # container image
```

Both stamp the version from `git describe`.
The image is a static binary on distroless, running as UID 65532.
To pass extra arguments during `docker build`:

```sh
just build --platform linux/arm64
```

To render the chart from source, pass extra arguments to `helm template`:

```sh
just template --set deployment.image.tag=`git describe --tags --always --dirty`
```

The chart in the repository only gets its real version on release,
so set `deployment.image.tag` when rendering it from source.

## Deploying the Helm chart

Every release publishes the chart to GHCR as an OCI artifact at
`oci://ghcr.io/jetbrains-research/charts/crusoe-registry-token-issuer`.
The chart version is the release tag without the `v`, so release `v0.1.0` ships chart `0.1.0`.
It does not manage credentials. You create the `Secret` yourself.

### Create the credentials `Secret`

The chart mounts it with `envFrom.secretRef`,
so the keys have to be named exactly like the environment variables,
and the `Secret` has to live in the release namespace.

```sh
kubectl create secret generic crusoe-secrets \
  --from-literal=CRUSOE_ACCESS_KEY=$CRUSOE_ACCESS_KEY \
  --from-literal=CRUSOE_API_ENDPOINT=$CRUSOE_API_ENDPOINT \
  --from-literal=CRUSOE_PROJECT_ID=$CRUSOE_PROJECT_ID \
  --from-literal=CRUSOE_SECRET_KEY=$CRUSOE_SECRET_KEY \
  --namespace crusoe-system
```

On a CMK cluster, the auto-provisioned secret may already carry `CRUSOE_ACCESS_KEY` and `CRUSOE_SECRET_KEY`.
Check with:

```sh
kubectl get secret -n crusoe-system
```

The `username` in every pull secret is the email of the user who owns this key pair.
Use a read-only key pair if your organization can mint one.

### Write a values file

```yaml
service:
  create: true

networkPolicy:
  create: true
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: external-secrets
          podSelector:
            matchLabels:
                app.kubernetes.io/name: external-secrets
      ports:
        - port: 5001
```

Once the `NetworkPolicy` exists, only traffic matching the `ingress` / `egress` rules is allowed.
An empty `networkPolicy.ingress` allows all ingress, and an empty `networkPolicy.egress` allows all egress.
If both are empty, `policyTypes` defaults to `Ingress` and Kubernetes blocks all incoming traffic.

Empty strings and `false` under `config` are left out of the `ConfigMap` entirely,
so the binary's own defaults apply. Set only what you want to override.

The chart doesn't set `deployment.terminationGracePeriodSeconds` from `config.server.timeout.shutdown`.
If you raise the shutdown timeout, raise the grace period above it too.
Otherwise, Kubernetes kills the `Pod` before in-flight requests finish, and the tokens minted for them are orphaned.

To see every value with its default:

```sh
helm show values oci://ghcr.io/jetbrains-research/charts/crusoe-registry-token-issuer --version 0.1.0
```

### Install

```sh
helm upgrade --install crusoe-registry-token-issuer oci://ghcr.io/jetbrains-research/charts/crusoe-registry-token-issuer \
  --version 0.1.0 \
  --namespace crusoe-system \
  --values values.prod.yaml
```

Without `--version`, Helm installs the latest release.
Render without applying to check the output first:

```sh
helm template crusoe-registry-token-issuer oci://ghcr.io/jetbrains-research/charts/crusoe-registry-token-issuer \
  --version 0.1.0 \
  --namespace crusoe-system \
  --values values.prod.yaml
```

Point the `ClusterGenerator` at the `Service` instead of `localhost`,
for example `http://crusoe-registry-token-issuer.crusoe-system.svc:5001/api/v1/token`.

### Chart values

| Value                                      | Default                                                         | Description                                                                                       |
|--------------------------------------------|-----------------------------------------------------------------|---------------------------------------------------------------------------------------------------|
| `config.registry`                          | `"registry.*.ccr.crusoecloudcompute.com"`                       | Maps to `CRUSOE_ISSUER_REGISTRY`.                                                                 |
| `config.server.*`                          | See the table above.                                            | Maps to `CRUSOE_ISSUER_SERVER_*`.                                                                 |
| `config.token.*`                           | See the table above.                                            | Maps to `CRUSOE_ISSUER_TOKEN_*`.                                                                  |
| `config.log.*`                             | See the table above.                                            | Maps to `CRUSOE_ISSUER_LOG_*`.                                                                    |
| `deployment.secretName`                    | `"crusoe-secrets"`                                              | Existing `Secret` with `CRUSOE_ACCESS_KEY` and `CRUSOE_SECRET_KEY`.                               |
| `deployment.imagePullSecrets`              | `[]`                                                            | Pull secrets for a private registry.                                                              |
| `deployment.image.repository`              | `ghcr.io/jetbrains-research/crusoe-registry-token-issuer`       | Image repository.                                                                                 |
| `deployment.image.tag`                     | `""`                                                            | Image tag. Falls back to `v{{ .Chart.AppVersion }}`.                                              |
| `deployment.image.pullPolicy`              | `IfNotPresent`                                                  | Image pull policy.                                                                                |
| `deployment.annotations`                   | `{}`                                                            | Annotations applied to the `Deployment`.                                                          |
| `deployment.podAnnotations`                | `{}`                                                            | Annotations applied to each `Pod`.                                                                |
| `deployment.replicas`                      | `1`                                                             | `Pod` count. The issuer is stateless, so more replicas are safe.                                  |
| `deployment.strategy`                      | `{}`                                                            | `Deployment` update strategy. Kubernetes defaults to `RollingUpdate`.                             |
| `deployment.probes.*`                      |                                                                 | Tune `readiness` and `liveness`, each with `initialDelay`, `period`, `timeout`. Can be disabled.  |
| `deployment.terminationGracePeriodSeconds` | `30`                                                            | Seconds before `SIGKILL`. Keep above `config.server.timeout.shutdown`.                            |
| `deployment.resources`                     | 50m CPU and 32Mi memory requested, 64Mi memory limit.           | Container resources.                                                                              |
| `deployment.securityContext`               | All capabilities dropped with non-root user and read-only root. | Container security context.                                                                       |
| `deployment.env`                           | `[]`                                                            | Extra environment variables.                                                                      |
| `deployment.priorityClassName`             | Default priority or `0`.                                        | `Pod` scheduling priority.                                                                        |
| `deployment.nodeSelector`                  | `{}`                                                            | Node labels the `Pod` must match to be scheduled.                                                 |
| `deployment.tolerations`                   | `[]`                                                            | Taints the `Pod` tolerates.                                                                       |
| `deployment.topologySpreadConstraints`     | `[]`                                                            | Rules for spreading `Pod` replicas across nodes or zones.                                         |
| `deployment.affinity`                      | `{}`                                                            | `Node` and `Pod` affinity rules.                                                                  |
| `service.*`                                |                                                                 | Used to create and configure a dedicated `Service` for reaching the issuer.                       |
| `serviceAccount.*`                         |                                                                 | Used to create and configure a dedicated `ServiceAccount` that issuer will run as.                |
| `networkPolicy.*`                          |                                                                 | Used to create and configure a dedicated `NetworkPolicy` for limiting traffic from/to the issuer. |
| `podDisruptionBudget.*`                    |                                                                 | Used to create and configure a dedicated `PodDisruptionBudget` for high availability.             |

## License

See [LICENSE](LICENSE).

[ccr]: https://docs.crusoecloud.com/container-registry/overview/index.html
[eso]: https://external-secrets.io
[rotator]: https://github.com/crusoecloud/crusoe-registry-token-rotator-helm-charts
