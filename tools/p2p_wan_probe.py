"""Deploy two local P2P probes and an isolated public relay (requires paramiko).

Credentials are read only from P2P_TEST_SSH_PASSWORD. Pin the SSH host key in
--known-hosts before running. No existing chain service is stopped or replaced.
"""
import argparse
import concurrent.futures
import datetime
import json
import os
from pathlib import Path
import shlex
import subprocess
import time
import urllib.request
import warnings

warnings.filterwarnings("ignore", category=DeprecationWarning)
import paramiko

REMOTE = "/opt/ordexus-core/p2p-wan-probe"
UNIT = "ordexus-p2p-probe-wan"


def http(port, route="/status", body=None, timeout=40):
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request("http://127.0.0.1:%d%s" % (port, route), data=data,
                                     headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return json.load(response)


def connect(args):
    client = paramiko.SSHClient()
    client.load_system_host_keys()
    if args.known_hosts.exists():
        client.load_host_keys(str(args.known_hosts))
    client.connect(args.host, username=args.user, password=os.environ["P2P_TEST_SSH_PASSWORD"],
                   timeout=15, auth_timeout=15, look_for_keys=False, allow_agent=False)
    return client


def remote(client, command, timeout=40):
    stdin, stdout, stderr = client.exec_command(command, timeout=timeout)
    output, error = stdout.read().decode(), stderr.read().decode()
    code = stdout.channel.recv_exit_status()
    if code:
        raise RuntimeError("remote command failed (%d): %s\n%s" % (code, output, error))
    return output


def stop_local(state):
    # Verify executable and identity argument before touching a saved Windows PID.
    for node in state.get("nodes", []):
        command = (
            "$p = Get-CimInstance Win32_Process -Filter 'ProcessId = %d'\n"
            "if ($p -and $p.ExecutablePath -eq '%s' -and $p.CommandLine.Contains('%s')) "
            "{ Stop-Process -Id $p.ProcessId -ErrorAction Stop }"
        ) % (node["pid"], node["binary"].replace("'", "''"), node["identity"].replace("'", "''"))
        subprocess.run(["pwsh", "-NoProfile", "-Command", command], check=True,
                       creationflags=subprocess.CREATE_NO_WINDOW)


def run_load(directory, name, port, target, mode="relay", duration=20,
             concurrency=1, size=1024, interval=0, timeout=5000):
    result = http(port, "/load", {"target":target, "mode":mode, "duration_seconds":duration,
                  "concurrency":concurrency, "payload_bytes":size,
                  "interval_millis":interval, "timeout_millis":timeout}, duration + timeout/1000 + 20)
    (directory / (name + ".json")).write_text(json.dumps(result, indent=2))
    compact = {k:v for k,v in result.items() if k != "metrics"}
    print(name + " " + json.dumps(compact), flush=True)
    return result


def fault_test(args, client, state):
    directory = args.directory
    a,b = state["nodes"]
    evidence = []
    for kind in ["disconnect", "restart-relay"]:
        before = [http(a["control_port"]), http(b["control_port"])]
        with concurrent.futures.ThreadPoolExecutor(1) as pool:
            future = pool.submit(run_load, directory, state["protocol"] + "-fault-" + kind,
                                 a["control_port"], b["status"]["peer_id"], "relay", 45, 1, 1024, 100)
            time.sleep(8)
            injected = datetime.datetime.now(datetime.timezone.utc).isoformat()
            if kind == "disconnect":
                http(a["control_port"], "/disconnect", {"peer":state["relay"]["peer_id"]})
            else:
                remote(client, "systemctl restart %s.service" % UNIT)
            result = future.result()
        after = [http(a["control_port"]), http(b["control_port"])]
        evidence.append({"kind":kind, "injected_at":injected, "before":before, "after":after, "load":result})
    (directory / (state["protocol"] + "-fault-evidence.json")).write_text(json.dumps(evidence, indent=2))


def suite(args, client, state):
    a,b = state["nodes"]
    prefix = state["protocol"]
    for size in [1024,65536]:
        for source,target in [(a,b),(b,a)]:
            run_load(args.directory, "%s-%s-to-%s-%d" % (prefix,source["name"],target["name"],size),
                     source["control_port"],target["status"]["peer_id"],size=size,timeout=15000)
    with concurrent.futures.ThreadPoolExecutor(2) as pool:
        futures=[pool.submit(run_load,args.directory,prefix+"-soak-"+source["name"],source["control_port"],
                             target["status"]["peer_id"],"relay",args.duration,1,1024,100)
                 for source,target in [(a,b),(b,a)]]
        for future in futures: future.result()
    fault_test(args,client,state)
    if prefix == "quic":
        punch = http(a["control_port"],"/punch",{"target":b["status"]["peer_id"]})
        (args.directory / "suite-punch.json").write_text(json.dumps(punch,indent=2))
        print("punch " + json.dumps(punch),flush=True)
        if punch["success"]:
            run_load(args.directory,"suite-punched-direct",a["control_port"],b["status"]["peer_id"],"direct",size=65536)
        else:
            run_load(args.directory,"suite-punch-fallback",a["control_port"],b["status"]["peer_id"])


def deploy(args, client):
    directory = args.directory.resolve()
    directory.mkdir(parents=True, exist_ok=True)
    state_path = directory / "topology.json"
    if state_path.exists():
        stop_local(json.loads(state_path.read_text()))
    remote(client, "systemctl stop %s.service 2>/dev/null || true" % UNIT)
    remote(client, "install -d -m 0700 %s" % shlex.quote(REMOTE))
    binary_name = "p2pprobe-" + args.version
    with client.open_sftp() as sftp:
        sftp.put(str(directory / (binary_name + "-linux")), REMOTE + "/" + binary_name)
        sftp.chmod(REMOTE + "/" + binary_name, 0o755)
    # Use one test port for each transport; leave existing firewall rules intact.
    # Host/cloud firewalls are inspected separately; no broad rule changes here.
    port = 55101 if args.protocol == "quic" else 55102
    command = ["systemd-run", "--unit=" + UNIT, "--collect", "--property=Nice=10",
               "--property=MemoryMax=384M", "--property=RuntimeMaxSec=7200",
               "--property=WorkingDirectory=" + REMOTE,
               REMOTE + "/" + binary_name, "-relay-server", "-protocol", args.protocol,
               "-port", str(port), "-advertised-ip", args.host,
               "-control", "127.0.0.1:55111", "-identity", REMOTE + "/relay-identity.json"]
    remote(client, " ".join(shlex.quote(item) for item in command))
    for attempt in range(30):
        try:
            relay = json.loads(remote(client, "curl -fsS http://127.0.0.1:55111/status"))
            break
        except RuntimeError:
            time.sleep(0.5)
    else:
        raise RuntimeError("public probe did not start")
    executable = directory / (binary_name + ".exe")
    state = {"host": args.host, "protocol": args.protocol, "version": args.version,
             "remote_directory": REMOTE, "remote_unit": UNIT, "relay": relay, "nodes": []}
    for name, node_port, control_port in [("a", 55201, 55211), ("b", 55202, 55212)]:
        key = directory / (name + "-identity.json")
        log_path = directory / ("%s-%s-%s.log" % (args.version, args.protocol, name))
        child_environment = {k:v for k,v in os.environ.items() if k != "P2P_TEST_SSH_PASSWORD"}
        with log_path.open("ab") as log:
            process = subprocess.Popen([str(executable), "-protocol", args.protocol,
                                        "-port", str(node_port), "-control", "127.0.0.1:%d" % control_port,
                                        "-advertised-ip", args.local_ip, "-identity", str(key),
                                        "-relay", relay["address"]], cwd=str(directory),
                                       stdout=log, stderr=subprocess.STDOUT, env=child_environment,
                                       creationflags=subprocess.CREATE_NO_WINDOW)
        state["nodes"].append({"name":name, "pid":process.pid, "port":node_port,
                               "control_port":control_port, "binary":str(executable),
                               "identity":str(key), "log":str(log_path)})
        state_path.write_text(json.dumps(state, indent=2))
    ready = False
    for attempt in range(40):
        try:
            ready = True
            for node in state["nodes"]:
                node["status"] = http(node["control_port"])
                ready = ready and any(c["PeerID"] == relay["peer_id"] for c in node["status"]["connections"])
            if ready:
                break
        except (OSError, ValueError):
            pass
        time.sleep(0.5)
    state["relay"] = json.loads(remote(client, "curl -fsS http://127.0.0.1:55111/status"))
    state_path.write_text(json.dumps(state, indent=2))
    print(json.dumps(state, indent=2), flush=True)
    if not ready:
        raise RuntimeError("both local nodes must connect to the public relay; inspect the saved logs")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["deploy", "status", "stop", "restart-relay", "inspect", "fault", "suite"])
    parser.add_argument("--host", default="101.35.87.31")
    parser.add_argument("--user", default="root")
    parser.add_argument("--directory", type=Path, default=Path("tmp/p2p-wan"))
    parser.add_argument("--known-hosts", type=Path, default=Path("tmp/p2p-wan/known_hosts"))
    parser.add_argument("--version", choices=["baseline", "optimized"], default="optimized")
    parser.add_argument("--protocol", choices=["tcp", "quic"], default="quic")
    parser.add_argument("--local-ip", default="192.168.1.4")
    parser.add_argument("--duration", type=int, default=180, help="bidirectional soak seconds (suite)")
    args = parser.parse_args()
    client = connect(args)
    try:
        if args.action == "deploy":
            deploy(args, client)
        elif args.action == "status":
            print(remote(client, "curl -fsS http://127.0.0.1:55111/status"))
        elif args.action == "inspect":
            for cmd in ["ss -lntup", "iptables -S INPUT", "nft list ruleset", "uptime", "free -m"]:
                print(cmd + "\n" + remote(client, cmd), flush=True)
        elif args.action in ("fault", "suite"):
            state=json.loads((args.directory / "topology.json").read_text())
            if args.action == "fault": fault_test(args,client,state)
            else: suite(args,client,state)
        elif args.action == "stop":
            state = json.loads((args.directory / "topology.json").read_text())
            stop_local(state)
            print(remote(client, "systemctl stop %s.service" % UNIT))
        else:
            # The transient unit has no Restart policy; execute its exact saved command.
            command = remote(client, "systemctl show %s.service -p ExecStart --value" % UNIT)
            print(command)
            print(remote(client, "systemctl restart %s.service" % UNIT))
    finally:
        client.close()


if __name__ == "__main__":
    main()
