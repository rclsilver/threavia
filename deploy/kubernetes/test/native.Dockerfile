ARG BASE=threavia-backend-codex:kubernetes-dev
FROM ${BASE}
COPY --chmod=0755 fixture-provider /usr/local/bin/test-fixture
ENTRYPOINT ["/usr/local/bin/test-fixture", "--native-codex-smoke"]
