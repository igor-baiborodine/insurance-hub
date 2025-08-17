################################################################################
# Child Makefile for QA insurance-hub
################################################################################

.PHONY: help
help:
	@echo "QA insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: microk8s-install
microk8s-install: ## Install MicroK8S and alias kubectl to microk8s kubectl
	sudo snap install microk8s --classic
	sudo usermod -a -G microk8s $$USER
	sudo chown -f -R $$USER ~/.kube || true
	grep -qxF "alias kubectl='microk8s kubectl'" ~/.bash_aliases || echo "alias kubectl='microk8s kubectl'" >> ~/.bash_aliases
	@echo "MicroK8S installed. Please log out and log back in for group changes to take effect."
