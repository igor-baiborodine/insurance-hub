#!/usr/bin/env bash
set -euo pipefail

code --install-extension ryanolsonx.solarized && \
code --install-extension Continue.continue
code --install-extension vscjava.vscode-java-pack && \
code --install-extension vscjava.vscode-lombok && \
code --install-extension oracle-labs-graalvm.micronaut && \
code --install-extension ms-azuretools.vscode-docker && \
code --install-extension ms-kubernetes-tools.vscode-kubernetes-tools && \
code --install-extension confluentinc.vscode-confluent && \
code --install-extension ria.elastic && \
code --install-extension ms-ossdata.vscode-pgsql && \
code --install-extension yzhang.markdown-all-in-one && \
code --install-extension ms-vscode.makefile-tools && \
code --install-extension streetsidesoftware.code-spell-checker && \
code --install-extension eamodio.gitlens