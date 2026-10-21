#!/usr/bin/env python3
"""CLI dispatcher for Kubernetes and QA administrative workflows."""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

from cluster import (  # noqa: E402
    clean_local_storage,
    configure_minio,
    create_local_cluster,
    create_secret,
    create_zipkin_user,
    deploy_aggregate,
    deploy_operator,
    guard,
    manage_image,
    port_forward,
)
from qa import cluster_create, create_nodes, node_action, pull_kubeconfig  # noqa: E402
from runtime import (  # noqa: E402
    REPOSITORY_ROOT,
    TARGETS,
    AdminError,
    Commands,
    Kubernetes,
    cli_error,
    require,
    safe_name,
    secret_input,
    selected_target,
)

SECRET_INPUTS_BY_PROFILE = {
    "postgres": ("PG_SVC_USER_PWD",),
    "elasticsearch-copy": (),
    "minio-storage-user": ("MINIO_CONSOLE_ACCESS_KEY", "MINIO_CONSOLE_SECRET_KEY"),
    "minio-storage-config": ("MINIO_ROOT_USER_PWD",),
    "minio-service-user": ("MINIO_SVC_ACCESS_KEY", "MINIO_SVC_SECRET_KEY"),
    "mongodb-root": ("MONGO_ROOT_USER_PWD",),
    "mongodb-product": ("MONGO_PRODUCT_USER_PWD",),
    "zipkin": ("ZIPKIN_ES_USER_PWD",),
}
SECRET_INPUTS_BY_AGGREGATE = {
    "infra": (
        "PG_SVC_USER_PWD",
        "MINIO_CONSOLE_ACCESS_KEY",
        "MINIO_CONSOLE_SECRET_KEY",
        "MINIO_ROOT_USER_PWD",
        "MONGO_ROOT_USER_PWD",
        "MONGO_PRODUCT_USER_PWD",
    ),
    "services": ("MINIO_SVC_ACCESS_KEY", "MINIO_SVC_SECRET_KEY"),
}


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    guard_parser = subparsers.add_parser("guard")
    guard_parser.add_argument(
        "kind", choices=("environment", "data", "service", "monitoring", "flux", "service-name")
    )

    namespace = subparsers.add_parser("namespace")
    namespace.add_argument("action", choices=("ensure",))
    namespace.add_argument("kind", choices=("data", "service", "monitoring", "flux"))

    operator = subparsers.add_parser("operator")
    operator.add_argument("name", choices=("postgres", "eck", "kafka", "minio", "mongodb"))

    secret = subparsers.add_parser("secret")
    secret.add_argument(
        "profile",
        choices=(
            "postgres",
            "elasticsearch-copy",
            "minio-storage-user",
            "minio-storage-config",
            "minio-service-user",
            "mongodb-root",
            "mongodb-product",
            "zipkin",
        ),
    )

    minio = subparsers.add_parser("minio")
    minio.add_argument("action", choices=("bucket", "user-policy"))

    zipkin = subparsers.add_parser("zipkin-user")
    zipkin.add_argument("--role", default="zipkin_role")
    zipkin.add_argument("--user", default="zipkin_user")

    image = subparsers.add_parser("image")
    image.add_argument("action", choices=("load", "local-dev-load", "local-dev-unload", "qa-load"))

    aggregate = subparsers.add_parser("deploy")
    aggregate.add_argument("kind", choices=("infra", "services"))

    local = subparsers.add_parser("local")
    local.add_argument("action", choices=("create", "delete-storage", "port-forward"))

    qa_nodes = subparsers.add_parser("qa-nodes")
    qa_nodes.add_argument(
        "action",
        choices=("create", "suspend", "resume", "snapshot", "snapshots-list", "restore", "delete"),
    )

    qa_cluster = subparsers.add_parser("qa-cluster")
    qa_cluster.add_argument("action", choices=("create", "pull-kubeconfig"))
    return parser


def _target() -> Kubernetes:
    environment = os.environ.get("ENV_NAME", "")
    require(environment in TARGETS, "ENV_NAME must explicitly equal local-dev or qa")
    expected = TARGETS[environment]
    target = selected_target(
        environment,
        os.environ.get("CUR_CTX", expected.context),
        data_namespace=os.environ.get("DATA_NS", expected.data_namespace),
        service_namespace=os.environ.get("SVC_NS", expected.service_namespace),
        monitoring_namespace=os.environ.get("MONITORING_NS", expected.monitoring_namespace),
        flux_namespace=os.environ.get("FLUX_SYSTEM_NS", expected.flux_namespace),
    )
    return Kubernetes(Commands(), target)


def _values(secret_names: tuple[str, ...] = ()) -> dict[str, str]:
    ordinary_names = (
        "SVC_NAME",
        "MINIO_ROOT_USER",
        "COPY_SECRET_NS",
        "ZIPKIN_ES_USER",
    )
    values = {name: os.environ.get(name, "") for name in ordinary_names}
    values.update({name: secret_input(name) for name in secret_names})
    return values


def main(argv: list[str] | None = None) -> int:
    arguments = _parser().parse_args(argv)
    commands = Commands()
    if arguments.command == "qa-nodes":
        master_only = os.environ.get("MASTER_ONLY", "false").lower() == "true"
        if arguments.action == "create":
            create_nodes(commands, master_only)
        else:
            node_action(commands, arguments.action, os.environ.get("QA_SNAPSHOT_NAME", "baseline"))
        return 0
    if arguments.command == "qa-cluster":
        if arguments.action == "create":
            cluster_create(commands, os.environ.get("MASTER_ONLY", "false").lower() == "true")
        else:
            pull_kubeconfig(commands, Path(os.environ.get("KUBECONFIG_DEST", "~/.kube/config")))
        return 0

    if arguments.command == "guard" and arguments.kind == "service-name":
        safe_name(os.environ.get("SVC_NAME", ""), "SVC_NAME")
        return 0

    kube = _target()
    if arguments.command == "secret":
        values = _values(SECRET_INPUTS_BY_PROFILE[arguments.profile])
    elif arguments.command == "deploy":
        values = _values(SECRET_INPUTS_BY_AGGREGATE[arguments.kind])
    elif arguments.command == "minio":
        values = _values()
    else:
        values = {}
    if arguments.command == "guard":
        guard(kube, arguments.kind)
    elif arguments.command == "namespace":
        require(arguments.action == "ensure", "unsupported namespace action")
        guard(kube, arguments.kind)
        namespaces = {
            "data": kube.target.data_namespace,
            "service": kube.target.service_namespace,
            "monitoring": kube.target.monitoring_namespace,
            "flux": kube.target.flux_namespace,
        }
        kube.ensure_namespace(namespaces[arguments.kind])
    elif arguments.command == "operator":
        deploy_operator(kube, arguments.name)
    elif arguments.command == "secret":
        create_secret(kube, arguments.profile, values)
    elif arguments.command == "minio":
        if arguments.action == "bucket":
            configure_minio(
                kube,
                "bucket",
                values["SVC_NAME"],
                bucket=os.environ.get("BUCKET_NAME", ""),
            )
        else:
            supplied = Path(os.environ.get("POLICY_FILE", ""))
            require(str(supplied), "POLICY_FILE is required")
            policy_root = (REPOSITORY_ROOT / "k8s").resolve()
            policy = (
                supplied.resolve() if supplied.is_absolute() else (policy_root / supplied).resolve()
            )
            require(policy.is_relative_to(policy_root), "POLICY_FILE must remain under k8s")
            require(policy.is_file(), f"policy file does not exist: {policy}")
            configure_minio(kube, "user-policy", values["SVC_NAME"], policy_path=policy)
    elif arguments.command == "zipkin-user":
        create_zipkin_user(kube, arguments.role, arguments.user)
    elif arguments.command == "image":
        manage_image(kube, arguments.action, os.environ.get("SVC_NAME", ""))
    elif arguments.command == "deploy":
        deploy_aggregate(kube, arguments.kind, values)
    elif arguments.command == "local":
        storage = Path(
            os.environ.get("LOCAL_DEV_STORAGE", "~/tmp/data/local-dev-storage")
        ).expanduser()
        if arguments.action == "create":
            config = (
                REPOSITORY_ROOT / "k8s" / "bootstrap" / "local-dev" / "local-dev-insurance-hub.yaml"
            )
            create_local_cluster(commands, TARGETS["local-dev"], storage, config)
        elif arguments.action == "delete-storage":
            clean_local_storage(storage.resolve())
        else:
            port_forward(kube)
    else:
        raise AdminError(f"unsupported command {arguments.command}")
    return 0


def cli(argv: list[str] | None = None) -> int:
    return cli_error("k8s-admin", lambda: main(argv))


if __name__ == "__main__":
    raise SystemExit(cli())
