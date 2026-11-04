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


def is_non_negative_int(value):
    return type(value) is int and value >= 0


def assert_job_row(row):
    assert isinstance(row, dict), row
    for field in ("region", "vendor", "account", "itype", "jobid", "nextime", "runtime"):
        assert isinstance(row.get(field), str) and row[field], row
    assert row["itype"] in {"group", "ecs", "rds", "redis"}, row
    assert type(row.get("interval")) is int and row["interval"] > 0, row
    assert len(row["jobid"].split("/")) == (3 if row["itype"] == "group" else 4), row
    assert is_non_negative_int(row.get("on")) and is_non_negative_int(row.get("off")), row
    assert row["count"] == "无" or is_non_negative_int(row["count"]), row
    assert row["nextime"] == "无" or re.fullmatch(r"\d{2}\.\d{2}/\d{2}:\d{2}", row["nextime"]), row


def assert_resource_row(row):
    assert isinstance(row, dict), row
    for field in ("iid", "name", "region", "account", "vendor", "stat"):
        assert isinstance(row.get(field), str) and row[field], row


def main():
    base = sys.argv[1].rstrip("/")
    password = sys.argv[2]

    status, login = request(base, "POST", "/api/user/login", body={"username": "admin", "password": password})
    assert status == 200 and login["code"] == 20000, login
    token = login["data"]["token"]
    assert token.startswith("Bearer "), token

    _, info = request(base, "GET", "/api/user/info", token)
    assert (
        info["code"] == 20000
        and info["data"]["name"] == "admin"
        and info["data"]["avatar"] == "/harbor-mark.svg"
    ), info

    query = urllib.parse.quote(json.dumps({"vendor": "", "account": "", "itype": ""}, ensure_ascii=False))
    _, jobs = request(base, "GET", "/api/jobs?query_dict=" + query, token)
    assert jobs["code"] == 20000, jobs
    all_jobs = jobs["all_jobs"]
    assert isinstance(all_jobs, list) and all_jobs, jobs
    for row in all_jobs:
        assert_job_row(row)
    assert all(isinstance(jobs[key], list) for key in ("vendor_list", "account_list", "itype_list")), jobs
    assert jobs["vendor_list"] == sorted({row["vendor"] for row in all_jobs}), jobs
    assert jobs["account_list"] == sorted({row["account"] for row in all_jobs}), jobs
    assert jobs["itype_list"] == sorted({row["itype"] for row in all_jobs}), jobs

    ecs_jobs = [row for row in all_jobs if row["itype"] == "ecs"]
    assert ecs_jobs, jobs
    ecs = ecs_jobs[0]

    _, cloud = request(base, "GET", "/api/edit/cloud", token)
    assert cloud["code"] == 20000 and isinstance(cloud.get("cloud_dict"), dict), cloud
    expected_cloud = {}
    for row in all_jobs:
        vendor, account, *_ = row["jobid"].split("/")
        expected_cloud.setdefault(vendor, set()).add(account)
    assert all(
        isinstance(vendor, str)
        and isinstance(accounts, list)
        and all(isinstance(account, str) and account for account in accounts)
        for vendor, accounts in cloud["cloud_dict"].items()
    ), cloud
    assert {
        vendor: set(accounts)
        for vendor, accounts in cloud["cloud_dict"].items()
    } == expected_cloud, cloud

    _, res = request(base, "GET", "/api/nodes/res?job_id=" + urllib.parse.quote(ecs["jobid"], safe=""), token)
    assert res["code"] == 20000 and isinstance(res.get("res_list"), list), res
    for row in res["res_list"]:
        assert_resource_row(row)

    jms_query = urllib.parse.quote(json.dumps({"vendor": "", "account": ""}, ensure_ascii=False))
    _, jms = request(base, "GET", "/api/jms/list?query_dict=" + jms_query, token)
    assert jms["code"] == 20000 and isinstance(jms.get("ecs_list"), list), jms
    assert all(isinstance(jms.get(key), list) for key in ("vendor_list", "account_list")), jms
    assert jms["vendor_list"] == sorted({row["vendor"] for row in jms["ecs_list"]}), jms
    assert jms["account_list"] == sorted({row["account"] for row in jms["ecs_list"]}), jms
    for row in jms["ecs_list"]:
        assert isinstance(row, dict), row
        assert isinstance(row.get("vendor"), str) and row["vendor"], row
        assert isinstance(row.get("account"), str) and row["account"], row
        for field in ("count_linux", "count_win", "count_ecs", "count_on", "count_off"):
            assert is_non_negative_int(row.get(field)), row
        assert isinstance(row.get("count_mem"), str) and row["count_mem"].endswith("GB"), row
        assert isinstance(row.get("count_cpu"), str) and row["count_cpu"].endswith("核"), row
        assert type(row.get("sync")) is bool, row
        assert row["nextime"] == "无" or re.fullmatch(r"\d{2}\.\d{2}/\d{2}:\d{2}", row["nextime"]), row

    _, cfg = request(base, "GET", "/api/jms/config", token)
    assert cfg["code"] == 20000 and isinstance(cfg.get("jms_config"), dict), cfg
    assert "access_key_secret" not in cfg["jms_config"] and "token" not in cfg["jms_config"], cfg

    print("PASS: login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config")


if __name__ == "__main__":
    main()
