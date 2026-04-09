#!/usr/bin/env python3
"""Smoke 环境连接与目录状态检测脚本。"""

from __future__ import annotations

import argparse
import getpass
import os
import shlex
import socket
import sys
from dataclasses import dataclass

import paramiko


DEFAULT_HOST = "192.168.2.195"
DEFAULT_PORT = 22
DEFAULT_USER = "root"
DEFAULT_REMOTE_PATH = "/opt/EasyProxiesV2-smoke"


@dataclass
class CommandResult:
    code: int
    stdout: str
    stderr: str


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="测试 smoke 服务器 SSH 连接，并检查 /opt/EasyProxiesV2-smoke 的基本状态。",
    )
    parser.add_argument("--host", default=os.getenv("SMOKE_SSH_HOST", DEFAULT_HOST))
    parser.add_argument("--port", type=int, default=int(os.getenv("SMOKE_SSH_PORT", str(DEFAULT_PORT))))
    parser.add_argument("--user", default=os.getenv("SMOKE_SSH_USER", DEFAULT_USER))
    parser.add_argument("--password", default=os.getenv("SMOKE_SSH_PASSWORD"))
    parser.add_argument("--remote-path", default=os.getenv("SMOKE_REMOTE_PATH", DEFAULT_REMOTE_PATH))
    parser.add_argument("--timeout", type=int, default=10)
    return parser.parse_args()


def tcp_probe(host: str, port: int, timeout: int) -> None:
    with socket.create_connection((host, port), timeout=timeout):
        return


def run_remote(client: paramiko.SSHClient, command: str, timeout: int) -> CommandResult:
    stdin, stdout, stderr = client.exec_command(command, timeout=timeout)
    del stdin
    result = CommandResult(
        code=stdout.channel.recv_exit_status(),
        stdout=stdout.read().decode("utf-8", "ignore"),
        stderr=stderr.read().decode("utf-8", "ignore"),
    )
    return result


def detect_app_root(client: paramiko.SSHClient, remote_path: str, timeout: int) -> str:
    candidates = [remote_path, f"{remote_path}/app"]
    for candidate in candidates:
        compose_file = shlex.quote(f"{candidate}/docker-compose.yml")
        result = run_remote(client, f"test -f {compose_file}", timeout)
        if result.code == 0:
            return candidate
    raise RuntimeError(
        f"未在 {remote_path} 或 {remote_path}/app 下找到 docker-compose.yml，无法确认 smoke 应用目录。",
    )


def build_summary_command(app_root: str) -> str:
    quoted = shlex.quote(app_root)
    return f"""set -e
cd {quoted}
echo "APP_ROOT=$PWD"
echo "HAS_DOCKER_COMPOSE=$(test -f docker-compose.yml && echo 1 || echo 0)"
echo "HAS_CONFIG=$(test -f config.yaml && echo 1 || echo 0)"
echo "HAS_DATA=$(test -d data && echo 1 || echo 0)"
echo "DIRECTORY_LISTING_BEGIN"
ls -la | sed -n '1,20p'
echo "DIRECTORY_LISTING_END"
echo "MANAGEMENT_CONFIG_BEGIN"
awk '/^management:/{{flag=1;print;next}} /^[^[:space:]]/{{if(flag) exit}} flag{{print}}' config.yaml 2>/dev/null || true
echo "MANAGEMENT_CONFIG_END"
echo "DOCKER_COMPOSE_PS_BEGIN"
docker compose ps 2>&1 || true
echo "DOCKER_COMPOSE_PS_END"
echo "DOCKER_PS_BEGIN"
docker ps --format '{{{{.Names}}}}\\t{{{{.Image}}}}\\t{{{{.Status}}}}' 2>/dev/null || true
echo "DOCKER_PS_END"
"""


def print_block(title: str, body: str) -> None:
    print(f"\n[{title}]")
    text = body.strip()
    if text:
        print(text)
    else:
        print("(empty)")


def main() -> int:
    args = parse_args()
    password = args.password or getpass.getpass(f"SSH password for {args.user}@{args.host}: ")

    print(f"[1/4] TCP probing {args.host}:{args.port} ...")
    try:
        tcp_probe(args.host, args.port, args.timeout)
    except OSError as exc:
        print(f"TCP 连接失败: {exc}", file=sys.stderr)
        return 2
    print("TCP 连接成功")

    print(f"[2/4] SSH connecting {args.user}@{args.host} ...")
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    try:
        client.connect(
            hostname=args.host,
            port=args.port,
            username=args.user,
            password=password,
            timeout=args.timeout,
            banner_timeout=args.timeout,
            auth_timeout=args.timeout,
            look_for_keys=False,
            allow_agent=False,
        )
    except Exception as exc:  # noqa: BLE001
        print(f"SSH 登录失败: {exc}", file=sys.stderr)
        return 3

    try:
        print(f"[3/4] Detecting app root under {args.remote_path} ...")
        app_root = detect_app_root(client, args.remote_path, args.timeout)
        print(f"应用目录: {app_root}")

        print("[4/4] Collecting remote summary ...")
        result = run_remote(client, build_summary_command(app_root), max(args.timeout, 20))
        if result.code != 0:
            print(result.stdout)
            print(result.stderr, file=sys.stderr)
            print(f"远端检查命令失败，exit code={result.code}", file=sys.stderr)
            return 4

        markers = {
            "DIRECTORY_LISTING": ("DIRECTORY_LISTING_BEGIN", "DIRECTORY_LISTING_END"),
            "MANAGEMENT_CONFIG": ("MANAGEMENT_CONFIG_BEGIN", "MANAGEMENT_CONFIG_END"),
            "DOCKER_COMPOSE_PS": ("DOCKER_COMPOSE_PS_BEGIN", "DOCKER_COMPOSE_PS_END"),
            "DOCKER_PS": ("DOCKER_PS_BEGIN", "DOCKER_PS_END"),
        }

        lines = result.stdout.splitlines()
        simple_lines: list[str] = []
        blocks: dict[str, list[str]] = {key: [] for key in markers}
        current_block: str | None = None

        begin_lookup = {begin: key for key, (begin, _) in markers.items()}
        end_lookup = {end: key for key, (_, end) in markers.items()}

        for line in lines:
            if line in begin_lookup:
                current_block = begin_lookup[line]
                continue
            if line in end_lookup:
                current_block = None
                continue
            if current_block is None:
                simple_lines.append(line)
            else:
                blocks[current_block].append(line)

        print_block("SUMMARY", "\n".join(simple_lines))
        for key in ("DIRECTORY_LISTING", "MANAGEMENT_CONFIG", "DOCKER_COMPOSE_PS", "DOCKER_PS"):
            print_block(key, "\n".join(blocks[key]))

        if result.stderr.strip():
            print_block("STDERR", result.stderr)
    finally:
        client.close()

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
