#!/usr/bin/env bash
# Tear down the YuSui demo. By default removes the host-docker server side and
# stops the agent in the VM (keeps the VMs so a re-run of up.sh is fast).
# Pass --vms to also delete the OrbStack VMs, --netbird to also stop the NetBird
# control plane.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

echo "removing docker server side + asset containers…"
for c in demo-web demo-yusui-srv demo-server demo-pg demo-prod-db demo-prod-app; do docker rm -f "$c" >/dev/null 2>&1; done
docker network rm demonet assetnet >/dev/null 2>&1

echo "stopping the agent in yusui-agent…"
orb -m yusui-agent sudo bash -c 'systemctl disable --now yusui-agent 2>/dev/null; netbird down 2>/dev/null' >/dev/null 2>&1 || true

if [ "${1:-}" = "--vms" ] || [ "${2:-}" = "--vms" ]; then
  echo "deleting the agent VM…"
  orb delete -f yusui-agent >/dev/null 2>&1
fi
if [ "${1:-}" = "--netbird" ] || [ "${2:-}" = "--netbird" ]; then
  echo "stopping the NetBird control plane…"
  docker compose -f "$ROOT/deploy/netbird/docker-compose.yml" down >/dev/null 2>&1
fi
echo "done."
