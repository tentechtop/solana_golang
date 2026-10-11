#!/data/data/com.termux/files/usr/bin/bash
# Copy beside an existing four-node start.sh; this never generates a chain.
set -euo pipefail
export HOME=/data/data/com.termux/files/home
export PREFIX=/data/data/com.termux/files/usr
export PATH="$PREFIX/bin:/system/bin"
export TMPDIR="$PREFIX/tmp"
root=$(cd -- "$(dirname -- "$0")" && pwd -P)
cd "$root"
umask 077
mkdir -p logs pids
exec >>logs/boot.log 2>&1
echo "$(date -Iseconds) Ordexus boot startup: $root"

# Close this descriptor in children so the lock lasts only for startup.
exec 9>pids/boot.lock
if ! flock -n 9; then
  echo "Another boot startup is already running."
  exit 0
fi

for i in 1 2 3 4; do
  test -f "config/node$i.json"
done
test -x bin/ordexus-node

# PID files survive a reboot and their numbers can be reused. Identify each
# process by its executable and exact config argument before adopting its PID.
find_node() {
  local index=$1 proc executable candidate
  local -a argv=()
  found_pid=
  for proc in /proc/[0-9]*; do
    executable=$(readlink "$proc/exe" 2>/dev/null) || continue
    [ "$executable" = "$root/bin/ordexus-node" ] || continue
    argv=()
    mapfile -d '' -t argv <"$proc/cmdline" 2>/dev/null || continue
    [ "${#argv[@]}" -eq 3 ] || continue
    [ "${argv[1]}" = -config ] || continue
    [ "${argv[2]}" = "$root/config/node$index.json" ] || continue
    candidate=${proc##*/}
    if [ -n "$found_pid" ]; then
      echo "Refusing duplicate node$index processes: $found_pid, $candidate"
      return 1
    fi
    found_pid=$candidate
  done
}

declare -a node_pids=()
for i in 1 2 3 4; do
  find_node "$i"
  node_pids[$i]=$found_pid
  if [ -z "$found_pid" ]; then
    for port in "$((19400+i))" "$((29100+i))"; do
      if timeout 1 bash -c "true >/dev/tcp/127.0.0.1/$port" 2>/dev/null; then
        echo "Refusing startup: port $port belongs to an unidentified process."
        exit 1
      fi
    done
  fi
done

termux-wake-lock
export GOMAXPROCS=2 GOMEMLIMIT=384MiB GOGC=75
for i in 1 2 3 4; do
  if [ -n "${node_pids[$i]}" ]; then
    echo "node$i already running: PID ${node_pids[$i]}"
  else
    echo "$(date -Iseconds) Boot startup" >>"logs/node$i.log"
    nohup "$root/bin/ordexus-node" -config "$root/config/node$i.json" \
      >>"logs/node$i.log" 2>&1 </dev/null 9>&- &
    node_pids[$i]=$!
    echo "Started node$i: PID ${node_pids[$i]}"
  fi
  printf '%s\n' "${node_pids[$i]}" >"pids/node$i.pid"
done
sleep 3
for i in 1 2 3 4; do
  find_node "$i"
  if [ -z "$found_pid" ]; then
    echo "node$i exited; inspect logs/node$i.log."
    exit 1
  fi
done

# Start consensus immediately, then give Wi-Fi up to five minutes to return.
# Keep the existing specific listener address and client allowlist unchanged.
# This is a bounded startup retry, not a monitor that reverses manual stops.
if [ -f start-lan.sh ]; then
  for attempt in {1..50}; do
    for i in 1 2 3 4; do
      find_node "$i"
      if [ -z "$found_pid" ]; then
        echo "A node stopped during boot startup; stopping the LAN retry."
        exit 1
      fi
    done
    if bash start-lan.sh 9>&-; then
      echo "$(date -Iseconds) Four nodes and LAN RPC are running."
      exit 0
    fi
    sleep 5
  done
  echo "LAN RPC did not start. Check Wi-Fi IP and logs/lan-rpc.log; four nodes remain running."
  exit 1
fi
echo "$(date -Iseconds) Four nodes are running."
