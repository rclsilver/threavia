FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends bash git ca-certificates && rm -rf /var/lib/apt/lists/*
COPY threavia-executor threavia-backend-codex threavia-backend-claude execution-cluster-tests fixture-provider /usr/local/bin/
RUN ln -s /usr/local/bin/fixture-provider /usr/local/bin/fixture-codex && ln -s /usr/local/bin/fixture-provider /usr/local/bin/fixture-claude
RUN chmod 0755 /usr/local/bin/threavia-* /usr/local/bin/execution-cluster-tests /usr/local/bin/fixture-provider
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/threavia-executor"]
