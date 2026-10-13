#!/usr/bin/env bash
set -euo pipefail

# Separate explicit setup; validation targets never install dependencies.
mkdir -p .tools/bin
python3 -m venv --without-pip .tools/ci-venv
python3 -m pip --python .tools/ci-venv install --disable-pip-version-check 'PyYAML==6.0.3'
temporary_root="$(mktemp -d)"
trap 'rm -rf -- "${temporary_root}"' EXIT
curl --fail --location --retry 3 --max-time 120 \
  https://github.com/kubernetes-sigs/kind/releases/download/v0.30.0/kind-linux-amd64 \
  -o "${temporary_root}/kind-linux-amd64"
curl --fail --location --retry 3 --max-time 120 \
  https://github.com/kubernetes-sigs/kind/releases/download/v0.30.0/kind-linux-amd64.sha256sum \
  -o "${temporary_root}/kind.sha256sum"
(cd "${temporary_root}" && sha256sum --check kind.sha256sum)
curl --fail --location --retry 3 --max-time 120 \
  https://dl.k8s.io/release/v1.34.1/bin/linux/amd64/kubectl -o "${temporary_root}/kubectl"
curl --fail --location --retry 3 --max-time 120 \
  https://dl.k8s.io/release/v1.34.1/bin/linux/amd64/kubectl.sha256 -o "${temporary_root}/kubectl.sha256"
(cd "${temporary_root}" && printf '%s  kubectl\n' "$(cat kubectl.sha256)" | sha256sum --check)
install -m 0755 "${temporary_root}/kind-linux-amd64" .tools/bin/kind
install -m 0755 "${temporary_root}/kubectl" .tools/bin/kubectl
curl --fail --location --retry 3 --max-time 120 \
  https://github.com/rhysd/actionlint/releases/download/v1.7.7/actionlint_1.7.7_linux_amd64.tar.gz \
  -o "${temporary_root}/actionlint_1.7.7_linux_amd64.tar.gz"
curl --fail --location --retry 3 --max-time 120 \
  https://github.com/rhysd/actionlint/releases/download/v1.7.7/actionlint_1.7.7_checksums.txt \
  -o "${temporary_root}/actionlint-checksums.txt"
(cd "${temporary_root}" && sha256sum --check --ignore-missing actionlint-checksums.txt)
tar -xzf "${temporary_root}/actionlint_1.7.7_linux_amd64.tar.gz" -C "${temporary_root}" actionlint
install -m 0755 "${temporary_root}/actionlint" .tools/bin/actionlint
