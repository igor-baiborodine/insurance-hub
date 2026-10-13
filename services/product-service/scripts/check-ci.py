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
    require(checks['permissions'] == {'contents': 'read'}, 'PR permissions must be read-only')
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
    runs = '\n'.join(step.get('run', '') for step in job['steps'])
    require('secrets.' not in str(job) and 'login-action' not in str(job), 'PR may not receive publishing credentials')
    allowed = {'make go-product-bootstrap-tools', 'make -C services/product-service bootstrap-ci-tools',
               'make -C services/product-service check-ci', 'make go-svc-release-test SVC_NAME=product',
               'make go-svc-dispatch-test', 'make go-svc-deploy-check SVC_NAME=product',
               'make go-svc-deploy-test-tooling SVC_NAME=product', 'make postgres-svc-role-test',
               'make product-baseline-fixtures-check', 'make product-baseline-test BASELINE_SUITE=db-permissions',
               'make go-svc-image-check SVC_NAME=product', 'make go-svc-deploy-test-ci SVC_NAME=product',
               'echo "$GITHUB_WORKSPACE/services/product-service/.tools/bin" >> "$GITHUB_PATH"'}
    require(set(filter(None, runs.splitlines())) == allowed, 'PR commands differ from non-publishing validation contract')
    require(set(release['on']) == {'workflow_dispatch'}, 'release must be explicitly dispatched')
    require(release['permissions'] == {'contents': 'read'}, 'release default permissions changed')
    publish = release['jobs']['publish']
    require(publish['permissions'] == {'contents': 'read', 'packages': 'write'}, 'publication privileges differ')
    require(publish['if'] == "github.ref == 'refs/heads/main' && github.repository == 'igor-baiborodine/insurance-hub'",
            'release must run from canonical main')
    require(release['concurrency']['cancel-in-progress'] == 'false', 'release must not cancel a publication')
    steps = publish['steps']
    checkout = steps[0]['with']
    require(checkout == {'ref': 'main', 'fetch-depth': '0', 'persist-credentials': 'false'}, 'trusted checkout differs')
    guard = steps[1]['run']
    require('[[ "$RELEASE_REVISION" =~ ^[0-9a-f]{40}$ ]]' in guard and
            guard.index('git merge-base --is-ancestor') < guard.index('git checkout --detach') < guard.index('make '),
            'selected code must not execute before SHA and ancestry validation')
    commands = '\n'.join(step.get('run', '') for step in steps)
    require('make go-svc-image-publish SVC_NAME=product' in commands, 'release must use owning Make interface')
    require(not any(word in commands for word in ('kubectl', 'git push', 'git commit', 'postgres-svc-role-provision')),
            'release must not deploy or edit desired state')
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
        lambda c, r: r['on'].update(pull_request={}),
        lambda c, r: r['jobs']['publish'].update({'if': 'true'}),
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
