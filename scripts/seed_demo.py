#!/usr/bin/env python3
"""Seed a Consul instance with the demo dataset used by consulmock.

Works against a running Consul (the compose stack, a local agent, or the
in-memory mock). When Consul runs with ACLs enabled, pass the management
token so the KV writes and service registrations are authorized.

Usage:
    python3 scripts/seed_demo.py [consul_url] [token]

    consul_url  defaults to http://127.0.0.1:18500/v1
    token       Consul ACL token; defaults to $CONSUL_TOKEN

The seeded AES skey matches internal/consulmock so the demo JumpServer token
decrypts with the same key the backend reads from Consul.
"""
import base64
import json
import os
import subprocess
import sys
import urllib.parse
import urllib.request

API = (sys.argv[1] if len(sys.argv) > 1 else os.environ.get("CONSUL_URL", "http://127.0.0.1:18500/v1")).rstrip("/")
TOKEN = sys.argv[2] if len(sys.argv) > 2 else os.environ.get("CONSUL_TOKEN", "")
SKEY = "0123456789abcdef0123456789abcdef"


def request(req):
    if TOKEN:
        req.add_header("X-Consul-Token", TOKEN)
    with urllib.request.urlopen(req, timeout=10) as resp:
        if resp.status != 200:
            raise RuntimeError(f"{req.method} {req.full_url}: HTTP {resp.status}")


def put(path, value):
    data = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
    req = urllib.request.Request(
        API + "/kv/" + urllib.parse.quote(path, safe="/"), data=data, method="PUT"
    )
    request(req)


def encrypt(plaintext):
    iv = os.urandom(16)
    proc = subprocess.run(
        ["openssl", "enc", "-aes-256-cbc", "-K", SKEY.encode().hex(), "-iv", iv.hex()],
        input=plaintext.encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
    )
    return base64.b64encode(iv + proc.stdout).decode()


def register(service_id, name, address, port, tags, meta):
    body = {"ID": service_id, "Name": name, "Address": address, "Port": port, "Tags": tags, "Meta": meta}
    req = urllib.request.Request(
        API + "/agent/service/register",
        data=json.dumps(body, ensure_ascii=False).encode(),
        method="PUT",
    )
    request(req)


seed = {
    "ConsulManager/assets/secret/skey": {"sk": SKEY},
    "ConsulManager/assets/alicloud/group/main": {"rg-default": "默认资源组"},
    "ConsulManager/jobs/alicloud/main/group": {
        "id": "alicloud/main/group", "func": "__main__:alicloud.group",
        "args": ["main"], "minutes": 60, "trigger": "interval", "replace_existing": True,
    },
    "ConsulManager/jobs/alicloud/main/ecs/cn-hangzhou": {
        "id": "alicloud/main/ecs/cn-hangzhou", "func": "__main__:alicloud.ecs",
        "args": ["main", "cn-hangzhou", False], "minutes": 10, "trigger": "interval", "replace_existing": True,
    },
    "ConsulManager/jobs/alicloud/main/rds/cn-hangzhou": {
        "id": "alicloud/main/rds/cn-hangzhou", "func": "__main__:alicloud.rds",
        "args": ["main", "cn-hangzhou"], "minutes": 20, "trigger": "interval", "replace_existing": True,
    },
    "ConsulManager/jobs/alicloud/main/redis/cn-hangzhou": {
        "id": "alicloud/main/redis/cn-hangzhou", "func": "__main__:alicloud.redis",
        "args": ["main", "cn-hangzhou"], "minutes": 20, "trigger": "interval", "replace_existing": True,
    },
    "ConsulManager/record/jobs/alicloud/main/group": {
        "count": 2, "update": "10.23/14:10", "status": 20000, "msg": "同步资源组成功！总数：2",
    },
    "ConsulManager/record/jobs/alicloud/main/ecs/cn-hangzhou": {
        "count": 2, "update": "10.23/14:12", "status": 20000, "on": 1, "off": 1,
        "msg": "ECS同步成功！总数：2，开机：1，关机：1",
    },
    "ConsulManager/jms/jobs/alicloud/main": {
        "id": "alicloud/main/jms", "func": "__main__:sync_jms.run",
        "args": ["alicloud", "main"], "minutes": 3, "trigger": "interval", "replace_existing": True,
    },
    "ConsulManager/record/jms/alicloud/main": {
        "count": 2, "update": "1023/14:10", "status": 20000, "msg": "同步资源成功！总数：2",
    },
    "ConsulManager/jms/jobs/alicloud/main/node_id": {
        "node_id": "00000000-0000-0000-0000-000000000001", "interval": 3,
    },
    "ConsulManager/jms/ecs_info": {
        "linux": [["ssh/22"], "root"], "windows": [["rdp/3389"], "Administrator"],
    },
    "ConsulManager/jms/custom_ecs_info": {},
    "ConsulManager/jms/jms_info": {
        "url": "https://jumpserver.example.com", "token": encrypt("demo-jumpserver-admin-token"), "ver": "V3",
    },
}
for path, value in seed.items():
    put(path, value)

register("i-demo0001", "alicloud_main_ecs", "10.0.0.11", 9100,
         ["ON", "linux", "cn-hangzhou"],
         {"iid": "i-demo0001", "name": "prod-web-01", "region": "华东1(杭州)",
          "group": "默认资源组", "instance": "10.0.0.11:9100", "account": "main",
          "vendor": "阿里云", "os": "linux", "cpu": "4核", "mem": "8GB",
          "exp": "按量", "stat": "on"})
register("i-demo0002", "alicloud_main_ecs", "10.0.0.12", 9182,
         ["OFF", "windows", "cn-hangzhou"],
         {"iid": "i-demo0002", "name": "prod-win-01", "region": "华东1(杭州)",
          "group": "默认资源组", "instance": "10.0.0.12:9182", "account": "main",
          "vendor": "阿里云", "os": "windows", "cpu": "8核", "mem": "16GB",
          "exp": "按量", "stat": "off"})
register("rm-demo0001", "alicloud_main_rds", "rm-demo0001.mysql.rds.aliyuncs.com", 3306,
         ["ON", "主实例", "MySQL 8.0", "cn-hangzhou"],
         {"iid": "rm-demo0001", "name": "prod-mysql", "region": "华东1(杭州)",
          "group": "默认资源组", "instance": "rm-demo0001.mysql.rds.aliyuncs.com:3306",
          "account": "main", "itype": "主实例", "vendor": "阿里云", "disk": "100GB",
          "cpu": "4核", "mem": "8GB", "ver": "MySQL 8.0", "domain": "rm-demo0001.mysql.rds.aliyuncs.com",
          "exp": "按量", "stat": "on"})
register("r-demo0001", "alicloud_main_redis", "r-demo0001.redis.rds.aliyuncs.com", 6379,
         ["ON", "主从", "Redis 6.0", "cn-hangzhou"],
         {"iid": "r-demo0001", "name": "prod-redis", "region": "华东1(杭州)",
          "group": "默认资源组", "instance": "r-demo0001.redis.rds.aliyuncs.com:6379",
          "account": "main", "itype": "主从", "vendor": "阿里云", "mem": "4GB",
          "ver": "Redis 6.0", "exp": "按量", "stat": "on", "ip": "10.0.0.21"})
print("seeded", len(seed), "keys and 4 services")
