# threavia-backend-claude

A Threavia BackendInstance running Claude Code in Kubernetes
(THREAVIA_SPEC_V1.md sections 7 and 29).

A backend is a separate deployment from Core, never a part of it. It opens the
outbound control stream to Core and owns the provider credentials, the native
provider sessions, the filesystem the agent works on and the local tools. Core
never connects to it, which is why the same chart describes a cluster backend
and why a laptop backend needs no chart at all.

## Install

Create a one-shot registration token on Core, then:

```sh
helm install laptop deploy/helm/threavia-backend-claude \
  --set core.address=threavia:9090 \
  --set core.api=http://threavia:8080 \
  --set core.registrationToken="$TOKEN" \
  --set provider.apiKey="$ANTHROPIC_API_KEY"
```

The backend registers once, stores its persistent credential on its volume and
never uses the registration token again.

## Persistence

This is a StatefulSet with one volume mounted at the home directory. It holds
three things that must survive a restart:

- the backend credential, so a replacement pod comes back as the same instance
  rather than registering a second one;
- the durable local execution state of section 10, so events buffered during a
  Core outage are still delivered afterwards;
- the native provider sessions, so a Run is resumable.

Disabling persistence is a development convenience and nothing else.

## Not a sandbox

`discoveryRoots` constrains automatic directory discovery and search. It is not
a security boundary: filesystem and process access are the real OS permissions
of this container (sections 11 and 28). Give the pod the access the work needs,
and no more.
