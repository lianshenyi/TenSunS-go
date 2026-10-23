#!/usr/bin/env python3
"""Appendix-B compatibility smoke checks for the Go backend."""
import json
import re
import sys
import urllib.parse
import urllib.request


def request(base, method, path, token=None, body=None):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = token
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=30) as resp:
        return resp.status, json.loads(resp.read())


def main():
    base = sys.argv[1].rstrip("/")
    password = sys.argv[2]

    status, login = request(base, "POST", "/api/user/login", body={"username": "admin", "password": password})
    assert status == 200 and login["code"] == 20000, login
    token = login["data"]["token"]
    assert token.startswith("Bearer "), token

    _, info = request(base, "GET", "/api/user/info", token)
    assert info["code"] == 20000 and info["data"]["name"] == "admin", info

    query = urllib.parse.quote(json.dumps({"vendor": "", "account": "", "itype": ""}, ensure_ascii=False))
    _, jobs = request(base, "GET", "/api/jobs?query_dict=" + query, token)
    assert jobs["code"] == 20000, jobs
    ecs = [row for row in jobs["all_jobs"] if row["jobid"] == "alicloud/main/ecs/cn-hangzhou"]
    assert len(ecs) == 1, jobs
    ecs = ecs[0]
    assert ecs["vendor"] == "阿里云" and ecs["region"] == "华东1(杭州)", ecs
    assert ecs["count"] == 2 and ecs["on"] == 1 and ecs["off"] == 1, ecs
    assert re.fullmatch(r"\d{2}\.\d{2}/\d{2}:\d{2}", ecs["nextime"]), ecs

    _, cloud = request(base, "GET", "/api/edit/cloud", token)
    assert cloud["cloud_dict"]["alicloud"] == ["main"], cloud

    _, res = request(base, "GET", "/api/nodes/res?job_id=" + urllib.parse.quote("alicloud/main/ecs/cn-hangzhou", safe=""), token)
    assert res["code"] == 20000 and len(res["res_list"]) == 2, res
    assert {row["iid"] for row in res["res_list"]} == {"i-demo0001", "i-demo0002"}, res

    jms_query = urllib.parse.quote(json.dumps({"vendor": "", "account": ""}, ensure_ascii=False))
    _, jms = request(base, "GET", "/api/jms/list?query_dict=" + jms_query, token)
    assert jms["code"] == 20000 and len(jms["ecs_list"]) == 1, jms
    row = jms["ecs_list"][0]
    assert row["count_ecs"] == 2 and row["count_mem"] == "24.0GB" and row["count_cpu"] == "12核", row
    assert row["sync"] is True and row["interval"] == "3分钟", row
    assert re.fullmatch(r"\d{2}\.\d{2}/\d{2}:\d{2}", row["nextime"]), row

    _, cfg = request(base, "GET", "/api/jms/config", token)
    assert cfg["code"] == 20000 and cfg["jms_config"]["token"] == "demo-jumpserver-admin-token", cfg

    print("PASS: login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config")


if __name__ == "__main__":
    main()
