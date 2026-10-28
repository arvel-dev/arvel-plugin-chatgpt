#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Check synthetic discovery format goldens; never inspect a personal config."""
import json
from pathlib import Path
import tomllib


def project_servers(config):
    servers = []
    for name, definition in config.get("mcp_servers", {}).items():
        server = {"name": name}
        for key in ("command", "args", "url"):
            if key in definition:
                server[key] = definition[key]
        environment = {key: {"secret": True} for key in definition.get("env", {})}
        for key in definition.get("env_vars", []):
            environment[key] = {"envRef": key}
        if environment:
            server["env"] = environment
        if "bearer_token_env_var" in definition:
            server["bearerToken"] = {"envRef": definition["bearer_token_env_var"]}
        servers.append(server)
    return servers


def main():
    root = Path(__file__).resolve().parent.parent / "testdata/fixtures/discovery"
    config = tomllib.loads((root / "config.toml").read_text())
    expected = json.loads((root / "expected.json").read_text())
    actual = project_servers(config)
    if actual != expected["servers"]:
        raise SystemExit("synthetic discovery golden mismatch")
    encoded = json.dumps(actual)
    for definition in config["mcp_servers"].values():
        for value in definition.get("env", {}).values():
            if value in encoded:
                raise SystemExit("synthetic credential survived projection")
    print("Synthetic TOML/envRef/secret goldens passed (not live RPC evidence).")


if __name__ == "__main__":
    main()
