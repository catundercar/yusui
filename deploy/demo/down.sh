#!/usr/bin/env bash
# Tear down the YuSui demo. By default removes all demo containers + networks.
# Pass --vms to also delete the leftover OrbStack agent VM (from the older
# VM-based demo, if any), --netbird to also stop the NetBird control plane.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

echo "removing demo containers (server side, agent, assets)…"
for c in demo-web demo-yusui-srv demo-server demo-pg demo-yusui-agent demo-agent demo-prod-db demo-prod-app; do docker rm -f "$c" >/dev/null 2>&1; done
docker network rm demonet assetnet >/dev/null 2>&1

if [ "${1:-}" = "--vms" ] || [ "${2:-}" = "--vms" ]; then
  echo "deleting the leftover agent VM (if any)…"
  orb delete -f yusui-agent >/dev/null 2>&1
fi
if [ "${1:-}" = "--netbird" ] || [ "${2:-}" = "--netbird" ]; then
  echo "stopping the NetBird control plane…"
  docker compose -f "$ROOT/deploy/netbird/docker-compose.yml" down >/dev/null 2>&1
fi
echo "done."
