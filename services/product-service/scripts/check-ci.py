#!/usr/bin/env python3
"""Check parsed workflow contracts and controlled corruptions, using pinned PyYAML."""
from copy import deepcopy
from fnmatch import fnmatchcase
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def validate(checks: dict, release: dict, modules: dict) -> None:
    require(set(checks['on']) == {'push', 'pull_request'}, 'checks must use push and pull_request only')
    require(checks['permissions'] == {'contents': 'read', 'packages': 'read'},
            'PR permissions must be read-only')
    job = checks['jobs']['validate']
    require(job['runs-on'] == 'ubuntu-24.04' and 'permissions' not in job and 'environment' not in job,
            'PR job may not elevate permissions or use deployment environment')
    for event in ('push', 'pull_request'):
        paths = checks['on'][event]['paths']
        require(checks['on'][event]['branches'] == ['main'], 'checks must target main')
        for example in ('services/product-service/Dockerfile', 'scripts/go/service/catalog.json',
                        'scripts/go/module/common.mk', 'scripts/postgres/service/catalog.json',
                        'scripts/product-deployment/reader.py', 'scripts/product-security/verify.sh',
                        'scripts/product-baseline/test.sh', 'k8s/Makefile', 'Makefile',
                        'k8s/apps/svc/product/base/deployment.yaml',
                        'k8s/overlays/qa/svc/kustomization.yaml',
                        'k8s/overlays/qa/svc/product/legacy/deployment-patch.yaml',
                        'k8s/overlays/local-dev/svc/product/deployment-patch.yaml',
                        'k8s/flux/qa/flux-system/kustomization-qa-svc.yaml',
                        'legacy/product-service/src/test/resources/product-read-baseline/catalog/qa.json',
                        'docs/migration/phase-4/product-service/deployment-runbook.md',
                        '.github/workflows/product-image-release.yml',
                        '.github/workflows/product-deployment.yml'):
            require(any(fnmatchcase(example, pattern) for pattern in paths), f'uncovered {event} path: {example}')
        require('docs/migration/phase-4/product-service/**' in modules['on'][event]['paths'],
                'module documentation gate must include runbook')
    java_steps = [step for step in job['steps'] if step.get('uses') == 'actions/setup-java@v5']
    require(len(java_steps) == 1 and java_steps[0].get('with') == {
        'java-version': '14', 'distribution': 'zulu', 'cache': 'maven', 'server-id': 'github',
        'server-username': 'GITHUB_ACTOR', 'server-password': 'GITHUB_TOKEN'},
        'PR checks must pin Java 14 and configure Maven package access')
    require(job.get('env') == {'GITHUB_TOKEN': '${{ secrets.GITHUB_TOKEN }}'},
            'PR Maven package token wiring differs')
    runs = '\n'.join(step.get('run', '') for step in job['steps'])
    job_without_package_token = str(job).replace('${{ secrets.GITHUB_TOKEN }}', '')
    require('secrets.' not in job_without_package_token and 'login-action' not in str(job),
            'PR may not receive publishing credentials')
    allowed = {'make go-product-bootstrap-tools', 'make -C services/product-service bootstrap-ci-tools',
               'make -C services/product-service check-ci', 'make go-svc-release-test SVC_NAME=product',
               'make go-svc-dispatch-test', 'make go-svc-deploy-check SVC_NAME=product',
               'make go-svc-deploy-test-tooling SVC_NAME=product', 'make postgres-svc-role-test',
               'make product-baseline-fixtures-check', 'make product-baseline-test BASELINE_SUITE=db-permissions',
               'make go-svc-image-check SVC_NAME=product', 'make go-svc-deploy-test-ci SVC_NAME=product',
               'echo "$GITHUB_WORKSPACE/services/product-service/.tools/bin" >> "$GITHUB_PATH"'}
    require(set(filter(None, runs.splitlines())) == allowed, 'PR commands differ from non-publishing validation contract')
    require(set(release['on']) == {'push', 'workflow_dispatch'},
            'release must use canonical-main push plus explicit recovery dispatch')
    trigger = release['on']['push']
    require(trigger == {'branches': ['main'], 'paths': [
        'services/product-service/**', '.github/workflows/product-image-release.yml']},
        'release paths must include Product source and its workflow only')
    dispatch = release['on']['workflow_dispatch']['inputs']['revision']
    require(dispatch.get('required') == 'true' and dispatch.get('type') == 'string',
            'recovery dispatch must require an explicit revision')
    require(release['permissions'] == {'contents': 'read'}, 'release default permissions changed')
    publish = release['jobs']['release']
    require(publish['permissions'] == {'contents': 'write', 'packages': 'write'},
            'release privileges differ')
    require(publish['if'] == "github.ref == 'refs/heads/main' && github.repository == 'igor-baiborodine/insurance-hub'",
            'release must run from canonical main')
    require(release['concurrency']['cancel-in-progress'] == 'false', 'release must not cancel a publication')
    steps = publish['steps']
    checkout = steps[0]['with']
    require(checkout == {'ref': 'main', 'fetch-depth': '0'}, 'trusted source checkout differs')
    require(publish['env']['RELEASE_REVISION'] ==
            "${{ github.event_name == 'workflow_dispatch' && inputs.revision || github.sha }}",
            'release revision selection differs')
    commands = '\n'.join(step.get('run', '') for step in steps)
    source_guard = steps[1]['run']
    require('[[ "$RELEASE_REVISION" =~ ^[0-9a-f]{40}$ ]]' in source_guard and
            source_guard.index('git merge-base --is-ancestor') <
            source_guard.index('git checkout --detach'),
            'selected source must be validated before checkout')
    require('npx -y monotag tag' in commands and '--path services/product-service' in commands and
            '--prefix services/product-service/v' in commands and '--min-version 1.0.0' in commands and
            '--max-version 1.999.999' in commands and
            'git tag --points-at "$RELEASE_REVISION"' in commands and
            'git cat-file -t "refs/tags/$existing_tag"' in commands,
            'scoped Product monotag or retry contract differs')
    require('make go-svc-image-release-check SVC_NAME=product' in commands and
            'make go-svc-release-test SVC_NAME=product' in commands and
            'make -C services/product-service check-ci' in commands and
            'make go-svc-image-check SVC_NAME=product' in commands,
            'release gates differ')
    require('make go-svc-image-publish SVC_NAME=product' in commands, 'release must use owning Make interface')
    require('make go-svc-release-finalize SVC_NAME=product' in commands,
            'release must finalize through the owning Make interface')
    require(commands.index('make go-svc-release-test') < commands.index('npx -y monotag tag') and
            commands.index('make go-svc-image-check') < commands.index('npx -y monotag tag') and
            commands.index('make go-svc-image-release-check') < commands.index('make go-svc-image-publish') <
            commands.index('make go-svc-release-finalize'), 'release gate or mutation order differs')
    require(not any(word in commands for word in ('kubectl', 'psql', 'postgres-svc-role-provision')),
            'release workflow must not directly deploy or provision infrastructure')
    require('k8s/overlays/qa/svc/product/**' not in trigger['paths'],
            'digest-only bot commits must not retrigger Product release')
    require(not any('image-publish' in step.get('run', '') for step in modules['jobs']['validate']['steps']),
            'module validation may not publish')


def main() -> None:
    def read(name: str) -> dict:
        return yaml.load((ROOT / '.github/workflows' / name).read_text(), Loader=yaml.BaseLoader)
    checks, release, modules = (read(name) for name in
                               ('product-deployment.yml', 'product-image-release.yml', 'go-modules.yml'))
    validate(checks, release, modules)
    corruptions = [
        lambda c, r: c['permissions'].update(packages='write'),
        lambda c, r: c['on']['pull_request']['paths'].remove('scripts/postgres/**'),
        lambda c, r: c['jobs']['validate']['steps'].append({'run': 'make go-svc-image-publish SVC_NAME=product'}),
        lambda c, r: r['on']['push']['paths'].append('k8s/overlays/qa/svc/product/**'),
        lambda c, r: r['jobs']['release'].update({'if': 'true'}),
    ]
    for corrupt in corruptions:
        bad_checks, bad_release = deepcopy(checks), deepcopy(release)
        corrupt(bad_checks, bad_release)
        try:
            validate(bad_checks, bad_release, modules)
        except ValueError:
            continue
        raise ValueError('controlled workflow corruption was accepted')
    print('check-ci: workflow YAML, permissions, triggers, Make boundaries and five corruptions passed')


if __name__ == '__main__':
    main()
