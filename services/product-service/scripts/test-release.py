#!/usr/bin/env python3
"""Release and runner setup boundary tests using synthetic external commands."""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ci_cluster
import release_image as release

REVISION = "a" * 40
SHORT = "a" * 7
VERSION = "1.2.3"
TAG = f"services/product-service/v{VERSION}"
IMAGE_ID = "sha256:" + "b" * 64
DIGEST = "sha256:" + "c" * 64
ENV = {"RELEASE_REVISION": REVISION, "RELEASE_VERSION": VERSION, "RELEASE_TAG": TAG,
       "GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main", "GITHUB_SHA": REVISION,
       "GITHUB_REPOSITORY": release.REPOSITORY}


class ReleaseTests(unittest.TestCase):
    def details(self, revision: str = REVISION, image_id: str = IMAGE_ID) -> str:
        return json.dumps([{"Id": image_id, "Os": "linux", "Architecture": "amd64",
                            "RepoDigests": [release.REGISTRY + "@" + DIGEST],
                            "Config": {"Labels": {"org.opencontainers.image.revision": revision}}}])

    def fake(self, argv: list[str], timeout: int = 60) -> str:
        if argv == ["git", "rev-parse", "--short", "HEAD"]:
            return SHORT
        if argv == ["git", "rev-parse", "HEAD"]:
            return REVISION
        if argv[:3] == ["docker", "image", "inspect"]:
            return IMAGE_ID if "--format" in argv else self.details()
        if argv[:2] == ["docker", "push"]:
            return f"pushed digest: {DIGEST} size: 1234"
        return ""

    def test_event_branch_repository_version_and_revision_reject_early(self) -> None:
        cases = [("GITHUB_EVENT_NAME", "pull_request"), ("GITHUB_REF", "refs/heads/feature"),
                 ("GITHUB_REPOSITORY", "fork/repo"), ("GITHUB_SHA", "d" * 40),
                 ("RELEASE_REVISION", "main"), ("RELEASE_VERSION", "2.0.0"),
                 ("RELEASE_TAG", "services/product-service/v1.2.4")]
        for key, value in cases:
            with self.subTest(key=key), patch.dict(os.environ, {**ENV, key: value}, clear=True), \
                 patch.object(release, "command", side_effect=self.fake) as command:
                with self.assertRaises(release.ReleaseError):
                    release.check()
                if key in {"GITHUB_EVENT_NAME", "GITHUB_REF", "GITHUB_REPOSITORY"}:
                    command.assert_not_called()

    def test_recovery_dispatch_and_canonical_push_are_accepted(self) -> None:
        for event in ("push", "workflow_dispatch"):
            values = {**ENV, "GITHUB_EVENT_NAME": event}
            if event == "workflow_dispatch":
                values.pop("GITHUB_SHA")
            with self.subTest(event=event), patch.dict(os.environ, values, clear=True), \
                 patch.object(release, "command", side_effect=self.fake):
                self.assertEqual(release.check(), REVISION)

    def test_unmerged_mismatched_and_dirty_revisions_cannot_publish(self) -> None:
        for failure in ("head", "ancestry", "dirty"):
            def command(argv: list[str], timeout: int = 60) -> str:
                if failure == "head" and argv == ["git", "rev-parse", "HEAD"]:
                    return "d" * 40
                if failure == "ancestry" and argv[:2] == ["git", "merge-base"]:
                    raise release.ReleaseError("not merged")
                if failure == "dirty" and argv[:2] == ["git", "status"]:
                    return "?? unreviewed-file"
                return self.fake(argv, timeout)
            with self.subTest(failure=failure), patch.dict(os.environ, ENV, clear=True), \
                 patch.object(release, "command", side_effect=command) as run:
                with self.assertRaises(release.ReleaseError):
                    release.publish()
                self.assertFalse(any(call.args[0][0] in ("docker", "make") for call in run.call_args_list))

    def test_success_publishes_semantic_and_unique_short_sha_tags_to_one_digest(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "metadata.json"
            with patch.dict(os.environ, {**ENV, "RELEASE_METADATA": str(output)}, clear=True), \
                 patch.object(release, "command", side_effect=self.fake) as run, \
                 patch.object(release, "existing_tag", return_value=None):
                release.publish()
            metadata = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(metadata["image"], release.REGISTRY + "@" + DIGEST)
            self.assertEqual(metadata["semanticTag"], release.REGISTRY + ":" + VERSION)
            self.assertEqual(metadata["shaTag"], release.REGISTRY + ":sha-" + SHORT)
            commands = [call.args[0] for call in run.call_args_list]
            validation = commands.index(["make", "go-svc-image-check", "SVC_NAME=product"])
            pushes = [index for index, value in enumerate(commands) if value[:2] == ["docker", "push"]]
            self.assertEqual(len(pushes), 2)
            self.assertTrue(all(validation < index for index in pushes))
            self.assertFalse(any(value[0] in ("kubectl", "psql") or value[:2] == ["git", "push"]
                                 for value in commands))

    def test_matching_existing_tags_make_publication_idempotent(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "metadata.json"
            with patch.dict(os.environ, {**ENV, "RELEASE_METADATA": str(output)}, clear=True), \
                 patch.object(release, "command", side_effect=self.fake) as run, \
                 patch.object(release, "existing_tag", return_value=DIGEST):
                release.publish()
            self.assertFalse(any(call.args[0][:2] == ["docker", "push"] for call in run.call_args_list))

    def test_existing_tag_collision_and_registry_errors_fail_closed(self) -> None:
        completed = subprocess.CompletedProcess(["docker"], 0, "{}", "")
        with patch.object(release, "execute", return_value=completed), \
             patch.object(release, "command", side_effect=lambda argv, timeout=60:
                          self.details(image_id="sha256:" + "d" * 64) if argv[:3] == ["docker", "image", "inspect"] else ""):
            with self.assertRaisesRegex(release.ReleaseError, "collision"):
                release.existing_tag(release.REGISTRY + ":1.2.3", IMAGE_ID, REVISION)
        failed = subprocess.CompletedProcess(["docker"], 1, "", "unauthorized")
        with patch.object(release, "execute", return_value=failed):
            with self.assertRaisesRegex(release.ReleaseError, "refusing"):
                release.existing_tag(release.REGISTRY + ":1.2.3", IMAGE_ID, REVISION)

    def test_failures_do_not_emit_success_metadata(self) -> None:
        for failure in ("validation", "push", "pull", "wrong-image"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "metadata.json"
                def command(argv: list[str], timeout: int = 60) -> str:
                    if (failure == "validation" and argv[0] == "make" or
                            failure == "push" and argv[:2] == ["docker", "push"] or
                            failure == "pull" and argv[:2] == ["docker", "pull"]):
                        raise release.ReleaseError("synthetic failure")
                    result = self.fake(argv, timeout)
                    if failure == "wrong-image" and argv[:3] == ["docker", "image", "inspect"] and "--format" not in argv:
                        return self.details(image_id="sha256:" + "d" * 64)
                    return result
                with patch.dict(os.environ, {**ENV, "RELEASE_METADATA": str(output)}, clear=True), \
                     patch.object(release, "command", side_effect=command), \
                     patch.object(release, "existing_tag", return_value=None):
                    with self.assertRaises(release.ReleaseError):
                        release.publish()
                    self.assertFalse(output.exists())

    def test_manifest_update_is_digest_only_validated_and_ordered(self) -> None:
        old = ("images:\n  - name: insurance-hub-product-api\n"
               f"    newName: {release.REGISTRY}\n    newTag: sha-unpublished\n")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest = root / "k8s/overlays/qa/svc/product/kustomization.yaml"
            manifest.parent.mkdir(parents=True)
            calls = []
            def command(argv: list[str], timeout: int = 60) -> str:
                calls.append(argv)
                if argv[:2] == ["git", "show"]:
                    return old.rstrip()
                if argv[:4] == ["git", "diff", "--cached", "--name-only"]:
                    return str(manifest.relative_to(root))
                return ""
            with patch.object(release, "ROOT", root), patch.object(release, "MANIFEST", manifest), \
                 patch.object(release, "command", side_effect=command):
                release.update_manifest({"revision": REVISION, "version": VERSION, "digest": DIGEST})
            self.assertIn("digest: " + DIGEST, manifest.read_text(encoding="utf-8"))
            self.assertLess(calls.index(["make", "go-svc-deploy-check", "SVC_NAME=product"]),
                            next(index for index, value in enumerate(calls) if value[:2] == ["git", "commit"]))
            self.assertLess(next(index for index, value in enumerate(calls) if value[:2] == ["git", "commit"]),
                            next(index for index, value in enumerate(calls) if value[:2] == ["git", "push"]))

    def test_existing_manifest_tag_and_release_require_exact_identity(self) -> None:
        current = release.desired_manifest(
            f"    newName: {release.REGISTRY}\n    newTag: old\n", DIGEST)
        self.assertEqual(release.desired_manifest(current, DIGEST), current)
        wrong_tag = subprocess.CompletedProcess(["git"], 0, "d" * 40 + "\n", "")
        with patch.object(release, "execute", return_value=wrong_tag):
            with self.assertRaisesRegex(release.ReleaseError, "tag collision"):
                release.ensure_tag(TAG, REVISION, VERSION)
        lightweight = subprocess.CompletedProcess(["git"], 0, REVISION + "\n", "")
        with patch.object(release, "execute", return_value=lightweight), \
             patch.object(release, "command", return_value="commit"):
            with self.assertRaisesRegex(release.ReleaseError, "not annotated"):
                release.ensure_tag(TAG, REVISION, VERSION)
        existing = {"tagName": TAG, "body": "different", "isDraft": False, "isPrerelease": False}
        response = subprocess.CompletedProcess(["gh"], 0, json.dumps(existing), "")
        with tempfile.TemporaryDirectory() as directory:
            body = Path(directory) / "body.md"
            body.write_text("expected", encoding="utf-8")
            with patch.object(release, "execute", return_value=response):
                with self.assertRaisesRegex(release.ReleaseError, "differs"):
                    release.ensure_github_release(TAG, body, REVISION, release.REGISTRY + "@" + DIGEST)


class ClusterTests(unittest.TestCase):
    def test_cleanup_failure_preserves_original_failure(self) -> None:
        def command(argv: list[str], timeout: int = 60) -> str:
            if argv[:3] == ["kind", "create", "cluster"]:
                raise release.ReleaseError("original setup failure")
            if argv[:3] == ["kind", "delete", "cluster"]:
                raise release.ReleaseError("cleanup failure")
            return ""
        with patch.dict(os.environ, {"GITHUB_ACTIONS": "true",
                "RUNNER_ENVIRONMENT": "github-hosted"}, clear=True), \
             patch.object(ci_cluster, "command", side_effect=command), patch("sys.stderr"):
            with self.assertRaisesRegex(release.ReleaseError, "original setup failure"):
                ci_cluster.main()
            self.assertNotIn("KUBECONFIG", os.environ)

    def test_local_and_existing_clusters_are_rejected_without_mutation(self) -> None:
        with patch.dict(os.environ, {}, clear=True), patch.object(ci_cluster, "command") as run:
            with self.assertRaises(release.ReleaseError):
                ci_cluster.main()
            run.assert_not_called()
        with patch.dict(os.environ, {"GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted"}, clear=True), \
             patch.object(ci_cluster, "command", return_value=ci_cluster.NAME) as run:
            with self.assertRaises(release.ReleaseError):
                ci_cluster.main()
            run.assert_called_once_with(["kind", "get", "clusters"])

    def test_cleanup_and_private_kubeconfig_on_setup_or_test_failure(self) -> None:
        for stage in ("setup", "test"):
            calls = []
            def command(argv: list[str], timeout: int = 60) -> str:
                calls.append(argv)
                if stage == "setup" and argv[:3] == ["kind", "create", "cluster"]:
                    raise release.ReleaseError("partial setup")
                if argv[0] == "kubectl":
                    self.assertNotEqual(os.environ["KUBECONFIG"], "/untouched/config")
                return ""
            with self.subTest(stage=stage), patch.dict(os.environ, {"GITHUB_ACTIONS": "true",
                    "RUNNER_ENVIRONMENT": "github-hosted", "KUBECONFIG": "/untouched/config"}, clear=True), \
                 patch.object(ci_cluster, "command", side_effect=command), \
                 patch.object(ci_cluster.subprocess, "run", side_effect=subprocess.TimeoutExpired("make", 1)):
                with self.assertRaises((release.ReleaseError, subprocess.TimeoutExpired)):
                    ci_cluster.main()
                self.assertEqual(os.environ["KUBECONFIG"], "/untouched/config")
            self.assertEqual(calls[-1][:4], ["kind", "delete", "cluster", "--name"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
