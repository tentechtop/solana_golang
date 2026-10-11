#!/data/data/com.termux/files/usr/bin/bash
# Copy into the existing chain directory beside start.sh and stop.sh, then run
# from the actual Termux terminal. This preserves the chain ID and all identities.
set -eu
umask 077
taskRoot=$(cd "$(dirname "$0")" && pwd -P)
taskBinary="$taskRoot/bin/ordexus-node.stake-payout"
taskHashFile="$taskRoot/bin/ordexus-node.stake-payout.sha256"
test -f "$taskRoot/config/node1.json"
test -f "$taskBinary"
(cd "$taskRoot/bin" && sha256sum -c "$taskHashFile")
taskChainID=$(sed -n 's/^[[:space:]]*"chain_id":[[:space:]]*"\([^"]*\)".*/\1/p' "$taskRoot/config/node1.json")
test -n "$taskChainID"
taskMaxEpoch=0
taskMaxSlot=0
taskEpochSlots=$(sed -n 's/^[[:space:]]*"epoch_slots":[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$taskRoot/config/node1.json")
case "$taskEpochSlots" in ''|*[!0-9]*) echo 'Invalid epoch slots'; exit 1;; esac
test "$taskEpochSlots" -gt 0
for taskIndex in 1 2 3 4; do
  taskConfig="$taskRoot/config/node$taskIndex.json"
  case "$(cat "$taskConfig")" in *stake_reward_exact_ratio_epoch*) echo 'Reward upgrade already configured; refusing to change its schedule'; exit 1;; esac
  taskOtherChain=$(sed -n 's/^[[:space:]]*"chain_id":[[:space:]]*"\([^"]*\)".*/\1/p' "$taskConfig")
  taskOtherEpochSlots=$(sed -n 's/^[[:space:]]*"epoch_slots":[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$taskConfig")
  test "$taskOtherChain" = "$taskChainID"
  test "$taskOtherEpochSlots" = "$taskEpochSlots"
  taskRPCPort=$(sed -n 's/^[[:space:]]*"rpc_port":[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$taskConfig")
  case "$taskRPCPort" in ''|*[!0-9]*) echo 'Invalid RPC port'; exit 1;; esac
  taskStatus=$(curl -fsS --max-time 8 -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"getNodeStatus","params":[]}' "http://127.0.0.1:$taskRPCPort/")
  taskStatusChain=$(printf '%s' "$taskStatus" | sed -n 's/.*"chain_id":"\([^"]*\)".*/\1/p')
  test "$taskStatusChain" = "$taskChainID"
  taskEpoch=$(printf '%s' "$taskStatus" | sed -n 's/.*"epoch_id":\([0-9][0-9]*\).*/\1/p')
  taskSlot=$(printf '%s' "$taskStatus" | sed -n 's/.*"head_slot":\([0-9][0-9]*\).*/\1/p')
  case "$taskEpoch:$taskSlot" in *[!0-9:]*|:*) echo 'Invalid node status'; exit 1;; esac
  test -n "$taskEpoch" && test -n "$taskSlot"
  test "$taskEpoch" -le "$taskMaxEpoch" || taskMaxEpoch=$taskEpoch
  test "$taskSlot" -le "$taskMaxSlot" || taskMaxSlot=$taskSlot
  taskPID=$(cat "$taskRoot/pids/node$taskIndex.pid")
  # ADB run-as cannot signal the app's processes on this phone. Fail BEFORE
  # changing anything unless running in Termux's normal process context.
  kill -0 "$taskPID" || { echo 'Run this script in the actual Termux terminal'; exit 1; }
done
taskActivation=$((taskMaxEpoch + 1))
taskRemaining=$((taskEpochSlots - (taskMaxSlot - 1) % taskEpochSlots))
test "$taskRemaining" -gt 16 || taskActivation=$((taskActivation + 1))
for taskIndex in 1 2 3 4; do
  sed "/\"epoch_slots\":/a\  \"stake_reward_exact_ratio_epoch\": $taskActivation," "$taskRoot/config/node$taskIndex.json" > "$taskRoot/config/node$taskIndex.json.stake-payout-next"
done
bash "$taskRoot/stop.sh"
taskBackup="$taskRoot/backups/stake-payout-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$taskBackup"
tar -C "$taskRoot" -cf "$taskBackup/state.tar" data config logs pids
cp "$taskRoot/bin/ordexus-node" "$taskBackup/ordexus-node"
for taskIndex in 1 2 3 4; do
  mv "$taskRoot/config/node$taskIndex.json.stake-payout-next" "$taskRoot/config/node$taskIndex.json"
done
cp "$taskBinary" "$taskRoot/bin/ordexus-node"
chmod 700 "$taskRoot/bin/ordexus-node"
printf '{"chain_id":"%s","activation_epoch":%s,"backup":"%s"}\n' "$taskChainID" "$taskActivation" "$taskBackup" > "$taskRoot/stake-payout-upgrade.json"
bash "$taskRoot/start.sh"
printf 'Reward ratio upgrade scheduled for epoch %s; backup: %s\n' "$taskActivation" "$taskBackup"
