#!/usr/bin/env python3
"""Service entry point; lifecycle orchestration lives in the shared Go service tooling."""

from product_deployment_checks import cli

if __name__ == "__main__":
    raise SystemExit(cli())
