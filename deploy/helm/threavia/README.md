# Threavia Core Helm chart

Deploys Threavia Core: the client HTTP/JSON + SSE API and the backend gRPC
control service.

The chart deploys **Core only**. PostgreSQL and the S3-compatible object storage
are dependencies you run yourself and point the chart at. BackendInstances
running in Kubernetes are separate deployments, each with its own small
persistent volume for local execution state.

## Install

```bash
helm install threavia ./deploy/helm/threavia \
  --namespace threavia --create-namespace \
  --set postgres.host=postgresql.databases.svc.cluster.local \
  --set postgres.existingSecret=threavia-postgres \
  --set auth.mode=oidc \
  --set auth.oidc.issuer=https://keycloak.example.com/realms/threavia \
  --set auth.oidc.clientId=threavia
```

See [`values.yaml`](values.yaml) for every setting.

## Things to get right

**Expose the backend endpoint.** Backends always dial Core, never the reverse,
so the gRPC service must be reachable from every laptop or pod running a
BackendInstance. Enable `ingress.grpc` with a controller that speaks gRPC and
imposes no idle timeout: the control stream is long-lived and mostly quiet
between heartbeats.

**Use TLS for remote access.** Either terminate it at the ingress, or enable
`grpcTLS` to terminate it on the Core listener itself.

**Keep the registration key.** With `backendRegistration.sharedKey.enabled` and
no `existingSecret`, the chart generates a 32-byte key on first install and
preserves it across upgrades by reading the installed secret back. That secret
is the source of truth and is annotated `helm.sh/resource-policy: keep`. Losing
it means every backend has to register again.

Because the generation reads the cluster, `helm template` and `--dry-run` cannot
see the installed key and will render a different one. That is expected: only a
real install or upgrade preserves it.

**Do not expose `auth.mode: none`.** It attributes every request to one user and
exists for local development.

## Secrets

Every credential can come from a secret you manage:

| Setting | Secret reference |
| --- | --- |
| PostgreSQL password | `postgres.existingSecret` |
| Object storage credentials | `s3.existingSecret` |
| Basic auth password | `auth.basic.existingSecret` |
| Backend shared registration key | `backendRegistration.sharedKey.existingSecret` |
| gRPC TLS certificate and key | `grpcTLS.existingSecret` |

Passing a literal value instead makes the chart create the secret for you, which
means it ends up in your values and in the release history.
