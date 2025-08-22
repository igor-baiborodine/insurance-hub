################################################################################
# Child Makefile common for local dev/qa insurance-hub
################################################################################

.PHONY: help
help:
	@echo "Common local dev/qa insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: kubectl-install
kubectl-install: ## Install the latest stable kubectl on Ubuntu
	@KUBECTL_VERSION=$$(curl -L -s https://dl.k8s.io/release/stable.txt); \
	if [ -z "$$KUBECTL_VERSION" ]; then \
	  echo "Failed to fetch latest kubectl version!"; \
	  exit 1; \
	fi; \
	curl -LO "https://dl.k8s.io/release/$${KUBECTL_VERSION}/bin/linux/amd64/kubectl"; \
	chmod +x kubectl; \
	echo "Moving kubectl to /usr/local/bin (you may be prompted for your password)..."; \
	sudo mv kubectl /usr/local/bin/kubectl; \
	kubectl version --client

.PHONY: kube-ps1-install
kube-ps1-install: ## Install kube-ps1 prompt helper on Ubuntu
	@if [ ! -d "$$HOME/.kube-ps1" ]; then \
		echo "Cloning kube-ps1 repository..."; \
		git clone https://github.com/jonmosco/kube-ps1.git $$HOME/.kube-ps1; \
	else \
		echo "kube-ps1 repository already cloned."; \
	fi
	@echo "Adding kube-ps1 initialization to your shell profile (~/.bashrc)..."
	@if ! grep -q 'kube-ps1.sh' $$HOME/.bashrc; then \
		echo '\n# kube-ps1 setup' >> $$HOME/.bashrc; \
		echo 'source $$HOME/.kube-ps1/kube-ps1.sh' >> $$HOME/.bashrc; \
		echo "export PS1='[\\u@\\h \\W \\$(__kube_ps1)]\\$ '" >> $$HOME/.bashrc; \
		echo "kube-ps1 initialization added to ~/.bashrc. Please reload your shell or run 'source ~/.bashrc'."; \
	else \
		echo "kube-ps1 already configured in ~/.bashrc."; \
	fi
