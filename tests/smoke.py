#!/usr/bin/env python3
"""Exercise an enrolled Mac Runner with the disposable Godot fixture."""

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import sys
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen
from uuid import uuid4
from zipfile import ZipFile


def token(path):
    value = os.environ.get("AGENTRIB_OPERATOR_TOKEN", "")
    if not value and path:
        for line in Path(path).read_text().splitlines():
            if line.startswith("AGENTRIB_OPERATOR_TOKEN="):
                return line.partition("=")[2].strip()
    if not value:
        raise RuntimeError("Set AGENTRIB_OPERATOR_TOKEN or provide --server-env")
    return value


class API:
    def __init__(self, origin, credential):
        self.origin = origin.rstrip("/") + "/api/v1"
        self.headers = {"Authorization": "Bearer " + credential, "Content-Type": "application/json"}

    def raw(self, path, payload=None):
        req = Request(self.origin + path, headers=self.headers)
        if payload is not None:
            req.data = json.dumps(payload).encode()
        try:
            with urlopen(req, timeout=25) as response:
                return response.read()
        except HTTPError as error:
            raise RuntimeError(f"HTTP {error.code} for {path}: {error.read().decode(errors='replace')}") from error

    def __call__(self, path, payload=None):
        return json.loads(self.raw(path, payload))

    def detail(self, run):
        return self("/runs/" + run["id"])


def wait_for(check, detail, seconds=300):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = check()
        if value:
            return value
        time.sleep(2)
    raise RuntimeError("Timed out waiting for " + detail)


def smoke(api, machine, project):
    def online():
        return next((m for m in api("/machines") if m["id"] == machine and project in m["projects"]
                     and {"codex", "godot"} <= set(m["executors"])), None)

    wait_for(online, "Codex and Godot on the machine", 60)
    workflow = api("/workflows/template")
    workflow.update(id="acceptance-" + uuid4().hex[:8], name="Mac deployment acceptance")
    for step in workflow["steps"]:
        if step["id"] == "build":
            step["prompt"] = "This is a disposable acceptance fixture. Create only qa_marker.txt at the project root with the exact content AGENTRIB_ACCEPTANCE followed by a newline. Use a file-edit tool; the Runner records the commit."
        if step["id"] == "qa":
            step["executor"] = "codex"
            step["prompt"] = "Read qa_marker.txt in the checked-out fixture and the Godot evidence in this task. Confirm whether it contains AGENTRIB_ACCEPTANCE and the evidence contains the headless PASS result. In the final answer include exactly ACCEPTANCE_QA_PASS when both pass."
    workflow = api("/workflows", workflow)
    execution_id = uuid4().hex
    path = "/executions/" + execution_id
    api("/executions", {"id": execution_id, "workflow_id": workflow["id"], "version": workflow["version"],
                        "machine_id": machine, "project": project,
                        "brief": "Validate the Agentrib deployment with a disposable Godot smoke project."})
    api(path + "/approve/brief", {})
    print(f"Workflow execution {execution_id}", flush=True)

    def finished(step):
        e = api(path)
        for run in e["runs"]:
            if run["step_id"] != step:
                continue
            if run["status"] in {"failed", "lost", "canceled"}:
                tail = api.detail(run)["events"][-12:]
                raise RuntimeError(f"{step} {run['status']}: " + "\n".join(event["text"] for event in tail))
            if run["status"] == "succeeded":
                print(f"{step}: {run['status']} {run['id']}", flush=True)
                return run
        return None

    build = wait_for(lambda: finished("build"), "Codex development")
    verify = wait_for(lambda: finished("tests"), "Godot test and web export")
    review = wait_for(lambda: finished("qa"), "Codex read-only QA")
    commit = build["result"]["commit"]
    if not all(run.get("result", {}).get("commit") == commit for run in (verify, review)):
        raise RuntimeError("Runs do not share the same Git commit")
    if "ACCEPTANCE_QA_PASS" not in "\n".join(event["text"] for event in api.detail(review)["events"] if event["kind"] == "agent"):
        raise RuntimeError("Codex did not return the expected QA verdict")
    artifacts = {}
    for artifact in verify["result"]["artifacts"]:
        data = api.raw(f"/runs/{verify['id']}/artifacts/{artifact['name']}")
        if hashlib.sha256(data).hexdigest() != artifact["sha256"] or len(data) != artifact["bytes"]:
            raise RuntimeError("Downloaded artifact checksum or size does not match")
        artifacts[artifact["name"]] = data
    if b"PASS: checked-out development revision" not in artifacts["godot.log"]:
        raise RuntimeError("Godot report is missing the acceptance assertion")
    with ZipFile(io.BytesIO(artifacts["godot-web.zip"])) as web:
        names = set(web.namelist())
        if "index.html" not in names or not any(name.endswith(".wasm") for name in names):
            raise RuntimeError("Godot Web export is incomplete")
    api(path + "/approve/release", {})
    wait_for(lambda: api(path)["status"] == "succeeded", "complete workflow", 20)
    print(f"PASS: Git {commit}, three matching runs, Godot evidence and Web bundle verified", flush=True)


def cancel_smoke(api, machine, project, commit):
    run_id = uuid4().hex
    path = "/runs/" + run_id
    api("/runs", {"id": run_id, "spec": {"machine_id": machine, "project": project, "executor": "godot",
                                           "title": "Cancellation acceptance", "prompt": "", "commit": commit}})

    def is_running():
        detail = api(path)
        status = detail["run"]["status"]
        if status in {"failed", "lost", "canceled", "succeeded"}:
            raise RuntimeError(f"Cancellation fixture exited early ({status}): {detail['events'][-12:]}")
        return any("Cancellation fixture is running" in event["text"] for event in detail["events"])

    wait_for(is_running, "real Godot test process", 90)
    if api(path + "/cancel", {})["status"] != "canceling":
        raise RuntimeError("Cancellation was not acknowledged as pending")
    wait_for(lambda: api(path)["run"]["status"] == "canceled", "Runner confirms process exit", 35)
    print(f"PASS: Mac Runner stopped the running Godot process ({run_id})", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--origin", default="http://127.0.0.1:8765")
    parser.add_argument("--server-env")
    parser.add_argument("--machine", required=True)
    parser.add_argument("--project", default="agentrib-smoke")
    parser.add_argument("--cancel-commit")
    args = parser.parse_args()
    api = API(args.origin, token(args.server_env))
    smoke(api, args.machine, args.project)
    if args.cancel_commit:
        cancel_smoke(api, args.machine, args.project, args.cancel_commit)


if __name__ == "__main__":
    try:
        main()
    except RuntimeError as err:
        print(err, file=sys.stderr)
        sys.exit(1)
