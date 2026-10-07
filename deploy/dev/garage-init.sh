#!/bin/sh
# One-time setup of the local Garage cluster.
#
# Garage ships unconfigured: it needs a cluster layout before it stores anything,
# and a bucket plus a key before Threavia can use it. This script is idempotent,
# so running it again on a configured cluster is harmless.
#
# Threavia only ever talks to a generic S3-compatible endpoint. Garage is one
# convenient local choice; nothing in Core or in the Helm chart assumes it.
set -eu

BUCKET="${THREAVIA_S3_BUCKET:-threavia}"
KEY_NAME="${THREAVIA_S3_KEY_NAME:-threavia-dev}"

# Garage logs its RPC handshake on stderr; only stdout is parsed here.
garage() { docker compose exec -T objectstore /garage "$@" 2>/dev/null; }

printf 'waiting for garage'
for _ in $(seq 1 60); do
	if garage status >/dev/null 2>&1; then
		printf '\n'
		break
	fi
	printf '.'
	sleep 1
done

# A single-node development cluster: one zone, one node, a small capacity.
if ! garage layout show | grep -q 'dev-node'; then
	# The node id comes first: --tag is variadic and would otherwise swallow it.
	garage layout assign "$(garage node id -q | cut -d@ -f1)" -z dev -c 1G -t dev-node >/dev/null
fi

# Garage prints the version to enact; reading it back beats guessing.
version="$(garage layout show | sed -n 's/.*layout apply --version \([0-9]*\).*/\1/p' | tail -1)"
if [ -n "$version" ]; then
	garage layout apply --version "$version" >/dev/null
	echo "cluster layout applied (version $version)"
fi

garage bucket info "$BUCKET" >/dev/null 2>&1 || garage bucket create "$BUCKET" >/dev/null
garage key info "$KEY_NAME" >/dev/null 2>&1 || garage key create "$KEY_NAME" >/dev/null
garage bucket allow --read --write --owner "$BUCKET" --key "$KEY_NAME" >/dev/null

cat <<MESSAGE

Object storage ready. Export these to point Core at it:

  export THREAVIA_S3_ENABLED=true
  export THREAVIA_S3_ENDPOINT=http://localhost:3900
  export THREAVIA_S3_REGION=garage
  export THREAVIA_S3_BUCKET=$BUCKET
MESSAGE
garage key info "$KEY_NAME" --show-secret |
	sed -n 's/^Key ID: *\(.*\)/  export THREAVIA_S3_ACCESS_KEY_ID=\1/p;s/^Secret key: *\(.*\)/  export THREAVIA_S3_SECRET_ACCESS_KEY=\1/p'
echo
