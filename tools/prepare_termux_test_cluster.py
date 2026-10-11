"""Prepare an isolated four-validator test chain for ARM64 Termux.

Build the node with GOOS=android GOARCH=arm64 CGO_ENABLED=0 first.
Validator identities are temporary; the genesis wallet is the shared public
account in config/local/genesis-account.json. Its private key is never copied.
This does not join, stop, or change any existing chain.
"""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import secrets
import shutil
import subprocess
from datetime import datetime


def run(args):
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or result.stdout.strip() or "Command failed")
    return result.stdout.strip()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--device", required=True, help="Explicit adb serial")
    parser.add_argument("--adb", default="adb")
    parser.add_argument("--deploy", action="store_true", help="Copy to initialized, debuggable Termux")
    args = parser.parse_args()
    # Keep the public ordexus-core workspace path instead of resolving its legacy junction.
    repo = Path(__file__).absolute().parents[1]
    binaries = repo / "tmp" / "mobile-tools"
    genesis_profile = json.loads((repo / "config" / "local" / "genesis-account.json").read_text(encoding="utf-8"))
    founder_address = genesis_profile["raw_public_key"]
    initial_supply = int(genesis_profile["initial_supply_lamports"])
    if not 0 < initial_supply <= (1 << 64) - 1:
        raise RuntimeError("Invalid genesis supply")
    android_binary = binaries / "ordexus-node-android-arm64"
    identity_binary = binaries / "ordexus-node-identity.exe"
    wallet_binary = binaries / "ordexus-wallet-cli.exe"
    for path in (android_binary, identity_binary, wallet_binary):
        if not path.is_file():
            raise RuntimeError(f"Missing build: {path}")
    chain = "ordexus-termux-" + datetime.now().strftime("%Y%m%d-%H%M%S") + "-" + secrets.token_hex(4)
    root = repo / "tmp" / chain
    remote = "/data/data/com.termux/files/home/" + chain
    for name in ("bin", "config", "data", "logs", "pids"):
        (root / name).mkdir(parents=True, exist_ok=False)
    shutil.copy2(android_binary, root / "bin" / "ordexus-node")
    # Keep all test seeds private to the ignored test directory; never print them.
    identities = {i: {purpose: secrets.token_hex(32) for purpose in ("peer", "staker", "validator", "consensus")} for i in range(1, 5)}
    peer_ids = {i: run([str(identity_binary), "-print-peer-id", identities[i]["peer"]]) for i in identities}
    run([str(wallet_binary), "new-key", "-out", str(root / "recipient-key.json")])
    recipient_address = run([str(wallet_binary), "address", "-key", str(root / "recipient-key.json")])
    validators = [dict(staker_seed=identities[i]["staker"], validator_seed=identities[i]["validator"],
                       consensus_seed=identities[i]["consensus"], peer_id=peer_ids[i],
                       stake_lamports=100_000_000, commission_bps=0) for i in identities]
    genesis = dict(initial_supply_lamports=initial_supply, treasury_address=founder_address,
                   funded_accounts=[dict(address=founder_address, lamports=1_000_000_000_000)],
                   initial_validators=validators)
    genesis_time = int(datetime.now().timestamp() * 1000)
    for i in identities:
        config = dict(node_mode="fullnode", chain_id=chain, environment="local", production=False,
                      node_name=f"ordexus-termux-node{i}", data_path=f"{remote}/data/node{i}",
                      listen_ip="127.0.0.1", listen_port=19400 + i, network="tcp",
                      advertised_ip="127.0.0.1", advertised_port=19400 + i,
                      rpc_enabled=True, rpc_listen_ip="127.0.0.1", rpc_port=29100 + i,
                      allow_insecure_p2p=False, peer_seed=identities[i]["peer"],
                      staker_seed=identities[i]["staker"], validator_seed=identities[i]["validator"],
                      consensus_seed=identities[i]["consensus"],
                      node_features=dict(bootnode_enabled=True, wallet_pairing_enabled=False),
                      validator_enabled=True, consensus_enabled=True, auto_register=False,
                      stake_lamports=100_000_000, slot_millis=1000, epoch_slots=256,
                      finality_depth=2, turbine_fanout=2, mempool_max_transactions=500,
                      transaction_leader_forward_slots=32, genesis_start_unix_millis=genesis_time,
                      consensus_policy=dict(multi_validator_threshold=4),
                      emergency_recovery=dict(enabled=True), genesis=genesis,
                      bootstrap_peers=[dict(peer_id=peer_ids[j], ip="127.0.0.1", port=19400 + j,
                                            network="tcp", role="bootnode", roles=["bootnode"],
                                            capabilities=["relay", "dht"],
                                            rpc_url=f"http://127.0.0.1:{29100+j}/")
                                       for j in identities if j != i])
        write_json(root / "config" / f"node{i}.json", config)
    env = f'''export HOME=/data/data/com.termux/files/home
export PREFIX=/data/data/com.termux/files/usr
export PATH="$PREFIX/bin:/system/bin"
export TMPDIR="$PREFIX/tmp"
cd {remote}
umask 077
'''
    start = '''#!/data/data/com.termux/files/usr/bin/bash
set -eu
''' + env + '''
export GOMAXPROCS=2 GOMEMLIMIT=384MiB GOGC=75
mkdir -p logs pids
for i in 1 2 3 4; do
  if [ -f "pids/node$i.pid" ]; then
    pid=$(cat "pids/node$i.pid")
    if kill -0 "$pid" 2>/dev/null; then
      echo "A saved node process is still alive; use status.sh or stop.sh first."
      exit 1
    fi
  fi
done
for port in 19401 19402 19403 19404 29101 29102 29103 29104; do
  if timeout 1 bash -c "true >/dev/tcp/127.0.0.1/$port" 2>/dev/null; then
    echo "Port $port is already occupied."
    exit 1
  fi
done
termux-wake-lock
for i in 1 2 3 4; do
  nohup ./bin/ordexus-node -config "$PWD/config/node$i.json" >"logs/node$i.log" 2>&1 </dev/null &
  echo $! >"pids/node$i.pid"
  echo "Started node$i PID $(cat "pids/node$i.pid"), RPC http://127.0.0.1:$((29100+i))/"
done
'''
    stop = '''#!/data/data/com.termux/files/usr/bin/bash
set -eu
''' + env + '''
for i in 1 2 3 4; do
  [ -f "pids/node$i.pid" ] || continue
  pid=$(cat "pids/node$i.pid")
  if kill -0 "$pid" 2>/dev/null; then
    cmd=$(tr '\\0' ' ' <"/proc/$pid/cmdline")
    case "$cmd" in
      *ordexus-node*"$PWD/config/node$i.json"*) kill -TERM "$pid"; echo "Stopping node$i PID $pid";;
      *) echo "Refusing to stop mismatched PID $pid"; exit 1;;
    esac
  fi
done
for attempt in $(seq 1 30); do
  alive=0
  for file in pids/node[1-4].pid; do
    pid=$(cat "$file")
    if kill -0 "$pid" 2>/dev/null; then alive=1; fi
  done
  [ "$alive" = 0 ] && break
  sleep 1
done
[ "$alive" = 0 ] || { echo "Nodes still shutting down; wake lock kept."; exit 1; }
termux-wake-unlock
'''
    status = '''#!/data/data/com.termux/files/usr/bin/bash
set -eu
''' + env + '''
for i in 1 2 3 4; do
  echo "node$i PID $(cat "pids/node$i.pid" 2>/dev/null || echo stopped)"
  curl --max-time 3 -sS -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"getHealth","params":[]}' "http://127.0.0.1:$((29100+i))/" || true
  echo
done
'''
    for name, content in (("start.sh", start), ("stop.sh", stop), ("status.sh", status)):
        with (root / name).open("w", encoding="utf-8", newline="\n") as stream:
            stream.write(content)
    manifest = dict(chain_id=chain, device=args.device, local_root=str(root), remote_root=remote,
                    android_binary_sha256=hashlib.sha256(android_binary.read_bytes()).hexdigest(),
                    founder_address=founder_address, genesis_wallet_address=genesis_profile["wallet_address"],
                    treasury_address=founder_address, initial_supply_lamports=str(initial_supply),
                    recipient_address=recipient_address,
                    nodes=[dict(index=i, peer_id=peer_ids[i], rpc_port=29100+i, p2p_port=19400+i) for i in identities])
    write_json(root / "manifest.json", manifest)
    if args.deploy:
        adb = [args.adb, "-s", args.device]
        if run(adb + ["shell", "getprop", "ro.product.cpu.abi"]) != "arm64-v8a":
            raise RuntimeError("This package requires ARM64")
        # shell staging holds only a binary/scripts. Config seeds go directly to the app UID.
        run(adb + ["shell", "run-as", "com.termux", "mkdir", "-p", remote])
        for directory in ("bin", "config", "data", "logs", "pids"):
            run(adb + ["shell", "run-as", "com.termux", "mkdir", "-p", f"{remote}/{directory}"])
        staging_binary = "/data/local/tmp/" + chain + "-node"
        run(adb + ["push", str(root / "bin" / "ordexus-node"), staging_binary])
        run(adb + ["shell", "chmod", "644", staging_binary])
        run(adb + ["shell", "run-as", "com.termux", "cp", staging_binary, remote + "/bin/ordexus-node"])
        run(adb + ["shell", "rm", staging_binary])
        for path in [*sorted((root / "config").glob("*.json")),
                     root / "start.sh", root / "stop.sh", root / "status.sh"]:
            target = remote + "/" + path.relative_to(root).as_posix()
            # ASCII stdin avoids Windows adb's text-mode corruption of binary input.
            subprocess.run(adb + ["shell", "-T", "run-as", "com.termux", "sh", "-c",
                                  f"'umask 077; base64 -d > {target}'"],
                           input=base64.b64encode(path.read_bytes()) + b"\n", check=True,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            copied_hash = run(adb + ["shell", "run-as", "com.termux", "sha256sum", target]).split()[0]
            if copied_hash != hashlib.sha256(path.read_bytes()).hexdigest():
                raise RuntimeError(f"Transferred file checksum mismatch: {path.name}")
        run(adb + ["shell", "run-as", "com.termux", "chmod", "700", remote + "/bin/ordexus-node",
                   remote + "/start.sh", remote + "/stop.sh", remote + "/status.sh"])
        remote_hash = run(adb + ["shell", "run-as", "com.termux", "sha256sum", remote + "/bin/ordexus-node"])
        if remote_hash.split()[0] != manifest["android_binary_sha256"]:
            raise RuntimeError("Transferred Android binary checksum mismatch")
        smoke = run(adb + ["shell", "run-as", "com.termux", remote + "/bin/ordexus-node",
                          "-print-peer-id", "ordexus-termux-smoke-public-seed"])
        if not smoke:
            raise RuntimeError("Android binary smoke check returned no peer ID")
    print(f"Manifest: {root / 'manifest.json'}")
    print(f"In Termux run: bash {remote}/start.sh")


if __name__ == "__main__":
    main()
