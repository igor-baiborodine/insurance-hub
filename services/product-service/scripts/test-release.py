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

REVISION = 'a' * 40
IMAGE_ID = 'sha256:' + 'b' * 64
DIGEST = 'sha256:' + 'c' * 64
ENV = {'RELEASE_REVISION': REVISION, 'GITHUB_EVENT_NAME': 'workflow_dispatch',
       'GITHUB_REF': 'refs/heads/main', 'GITHUB_REPOSITORY': 'igor-baiborodine/insurance-hub'}


class ReleaseTests(unittest.TestCase):
    def fake(self, argv: list[str], timeout: int = 60) -> str:
        if argv == ['git', 'rev-parse', 'HEAD']:
            return REVISION
        if argv[:3] == ['docker', 'image', 'inspect']:
            if '--format' in argv:
                return IMAGE_ID
            return json.dumps([{'Id': IMAGE_ID, 'Os': 'linux', 'Architecture': 'amd64',
                                'Config': {'Labels': {'org.opencontainers.image.revision': REVISION}}}])
        if argv[:2] == ['docker', 'push']:
            return 'sha-' + REVISION + ': digest: ' + DIGEST + ' size: 1234'
        return ''

    def test_event_branch_repository_and_revision_reject_before_external_commands(self) -> None:
        for key, value in [('GITHUB_EVENT_NAME', 'pull_request'), ('GITHUB_EVENT_NAME', 'push'),
                           ('GITHUB_REF', 'refs/heads/feature'), ('GITHUB_REPOSITORY', 'fork/repo'),
                           ('RELEASE_REVISION', 'main'), ('RELEASE_REVISION', '--all'),
                           ('RELEASE_REVISION', 'a' * 12)]:
            with self.subTest(key=key, value=value), patch.dict(os.environ, {**ENV, key: value}, clear=True), \
                 patch.object(release, 'command') as command:
                with self.assertRaises(release.ReleaseError):
                    release.check()
                command.assert_not_called()

    def test_unmerged_mismatched_and_dirty_revisions_cannot_publish(self) -> None:
        for failure in ('head', 'ancestry', 'dirty'):
            def command(argv: list[str], timeout: int = 60) -> str:
                if failure == 'head' and argv == ['git', 'rev-parse', 'HEAD']:
                    return 'd' * 40
                if failure == 'ancestry' and argv[:2] == ['git', 'merge-base']:
                    raise release.ReleaseError('not merged')
                if failure == 'dirty' and argv[:2] == ['git', 'status']:
                    return '?? unreviewed-file'
                return self.fake(argv, timeout)
            with self.subTest(failure=failure), patch.dict(os.environ, ENV, clear=True), \
                 patch.object(release, 'command', side_effect=command) as run:
                with self.assertRaises(release.ReleaseError):
                    release.publish()
                self.assertFalse(any(call.args[0][0] in ('docker', 'make') for call in run.call_args_list))

    def test_check_never_builds_publishes_or_contacts_cluster(self) -> None:
        with patch.dict(os.environ, ENV, clear=True), patch.object(release, 'command', side_effect=self.fake) as run:
            self.assertEqual(release.check(), REVISION)
            self.assertTrue(all(call.args[0][0] == 'git' for call in run.call_args_list))

    def test_success_records_digest_after_validation_and_pull(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'metadata.json'
            with patch.dict(os.environ, {**ENV, 'RELEASE_METADATA': str(output)}, clear=True), \
                 patch.object(release, 'command', side_effect=self.fake) as run:
                release.publish()
            metadata = json.loads(output.read_text())
            self.assertEqual(metadata['image'], release.REGISTRY + '@' + DIGEST)
            self.assertEqual(metadata['revision'], REVISION)
            commands = [call.args[0] for call in run.call_args_list]
            validation = commands.index(['make', 'go-svc-image-check', 'SVC_NAME=product'])
            push = commands.index(['docker', 'push', release.REGISTRY + ':sha-' + REVISION])
            self.assertLess(validation, push)
            self.assertTrue(any(cmd[:2] == ['docker', 'pull'] and cmd[-1] == metadata['image'] for cmd in commands))
            self.assertFalse(any(cmd[0] in ('kubectl', 'psql') or cmd[:2] == ['git', 'push'] for cmd in commands))

    def test_failures_do_not_emit_success_metadata(self) -> None:
        for failure in ('validation', 'push', 'pull', 'wrong-image'):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / 'metadata.json'
                def command(argv: list[str], timeout: int = 60) -> str:
                    if (failure == 'validation' and argv[0] == 'make' or
                        failure == 'push' and argv[:2] == ['docker', 'push'] or
                        failure == 'pull' and argv[:2] == ['docker', 'pull']):
                        raise release.ReleaseError('synthetic failure')
                    result = self.fake(argv, timeout)
                    if failure == 'wrong-image' and argv[:3] == ['docker', 'image', 'inspect'] and '--format' not in argv:
                        return result.replace(IMAGE_ID, 'sha256:' + 'd' * 64)
                    return result
                with patch.dict(os.environ, {**ENV, 'RELEASE_METADATA': str(output)}, clear=True), \
                     patch.object(release, 'command', side_effect=command) as run:
                    with self.assertRaises(release.ReleaseError):
                        release.publish()
                    self.assertFalse(output.exists())
                    if failure == 'validation':
                        self.assertFalse(any(call.args[0][0] == 'docker' for call in run.call_args_list))


class ClusterTests(unittest.TestCase):
    def test_cleanup_failure_preserves_original_failure(self) -> None:
        def command(argv: list[str], timeout: int = 60) -> str:
            if argv[:3] == ['kind', 'create', 'cluster']:
                raise release.ReleaseError('original setup failure')
            if argv[:3] == ['kind', 'delete', 'cluster']:
                raise release.ReleaseError('cleanup failure')
            return ''
        with patch.dict(os.environ, {'GITHUB_ACTIONS': 'true',
                'RUNNER_ENVIRONMENT': 'github-hosted'}, clear=True), \
             patch.object(ci_cluster, 'command', side_effect=command), \
             patch('sys.stderr'):
            with self.assertRaisesRegex(release.ReleaseError, 'original setup failure'):
                ci_cluster.main()
            self.assertNotIn('KUBECONFIG', os.environ)

    def test_local_and_existing_clusters_are_rejected_without_mutation(self) -> None:
        with patch.dict(os.environ, {}, clear=True), patch.object(ci_cluster, 'command') as run:
            with self.assertRaises(release.ReleaseError):
                ci_cluster.main()
            run.assert_not_called()
        with patch.dict(os.environ, {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted'}, clear=True), \
             patch.object(ci_cluster, 'command', return_value=ci_cluster.NAME) as run:
            with self.assertRaises(release.ReleaseError):
                ci_cluster.main()
            run.assert_called_once_with(['kind', 'get', 'clusters'])

    def test_cleanup_and_private_kubeconfig_on_setup_or_test_failure(self) -> None:
        for stage in ('setup', 'test'):
            calls = []
            def command(argv: list[str], timeout: int = 60) -> str:
                calls.append(argv)
                if stage == 'setup' and argv[:3] == ['kind', 'create', 'cluster']:
                    raise release.ReleaseError('partial setup')
                if argv[0] == 'kubectl':
                    self.assertNotEqual(os.environ['KUBECONFIG'], '/untouched/config')
                return ''
            with self.subTest(stage=stage), patch.dict(os.environ, {'GITHUB_ACTIONS': 'true',
                    'RUNNER_ENVIRONMENT': 'github-hosted', 'KUBECONFIG': '/untouched/config'}, clear=True), \
                 patch.object(ci_cluster, 'command', side_effect=command), \
                 patch.object(ci_cluster.subprocess, 'run', side_effect=subprocess.TimeoutExpired('make', 1)):
                with self.assertRaises((release.ReleaseError, subprocess.TimeoutExpired)):
                    ci_cluster.main()
                self.assertEqual(os.environ['KUBECONFIG'], '/untouched/config')
            self.assertEqual(calls[-1][:4], ['kind', 'delete', 'cluster', '--name'])


if __name__ == '__main__':
    unittest.main(verbosity=2)
