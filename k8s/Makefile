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
kube-ps1-install: ## Install kube-ps1 prompt helper (without modifying .bashrc) and show setup instructions
	@if [ ! -d "$$HOME/.kube-ps1" ]; then \
		echo "Cloning kube-ps1 repository into $$HOME/.kube-ps1..."; \
		git clone https://github.com/jonmosco/kube-ps1.git $$HOME/.kube-ps1; \
	else \
		echo "kube-ps1 repository already cloned in $$HOME/.kube-ps1."; \
	fi
	@echo ""; \
	echo "kube-ps1 installation complete!"; \
	echo ""; \
	echo "To enable kube-ps1, add the following lines to your ~/.bashrc or ~/.zshrc:"; \
	echo ""; \
	echo "  # kube-ps1 setup"; \
	echo "  source \$HOME/.kube-ps1/kube-ps1.sh"; \
	echo "  export PS1='[\\u@\\h \\W \\$(__kube_ps1)]\\$ '"; \
	echo ""; \
	echo "Then run 'source ~/.bashrc' (or 'source ~/.zshrc') to apply the changes."; \
	echo "Afterwards, your prompt will display the current Kubernetes context and namespace.";
