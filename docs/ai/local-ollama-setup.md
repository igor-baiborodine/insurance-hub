<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [Local Ollama setup](#local-ollama-setup)
  - [OS and hardware](#os-and-hardware)
  - [Install or upgrade Ollama](#install-or-upgrade-ollama)
    - [Monitoring GPU utilization](#monitoring-gpu-utilization)
    - [Minimal smoke test](#minimal-smoke-test)
    - [Notes and quirks](#notes-and-quirks)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Local Ollama setup

### OS and hardware

- OS: Ubuntu 24.04.4 LTS (noble).
- Kernel: `6.17.0-29-generic` (`uname -a`).
- CPU: Intel i7‑14650HX (8 P-cores + 8 E-cores, suitable for local inference). 
- GPU: NVIDIA GeForce RTX 4070 Laptop GPU, 8 GB VRAM (`nvidia-smi`). 
- RAM: 64 GB. 

To verify the GPU driver and CUDA stack:

- `nvidia-smi`
    - Confirms driver version (e.g., 595.58.03), CUDA version, and that the RTX 4070 Laptop GPU is visible and healthy.

### Install or upgrade Ollama

Ollama is installed using the official Linux install script and pinned to a specific version for reproducibility. [docs.ollama](https://docs.ollama.com/linux)

Example (install or upgrade to 0.24.0):

- Stop the service (if already running):
    - `sudo systemctl stop ollama`
- Install/upgrade:
    - `curl -fsSL https://ollama.com/install.sh | OLLAMA_VERSION=0.24.0 sh`
- Start and enable the service:
    - `sudo systemctl start ollama`
    - `sudo systemctl enable ollama`

Verification:

- `ollama --version`
    - Expected: `ollama version is 0.24.0`.
- `systemctl status ollama`
    - Expected: `active (running)` with `/usr/local/bin/ollama serve` as the main process.

#### Monitoring GPU utilization

To observe how models utilize the RTX 4070 VRAM and compute during inference:

- **Real-time monitor (Recommended)**:
    - `watch -n 0.5 nvidia-smi`
    - Use this to monitor `Volatile GPU-Util` and `Memory-Usage` while running benchmarks.
- **Specific utilization query**:
    - `nvidia-smi --query-gpu=utilization.gpu --format=csv`
    - Returns a simple percentage (e.g., `15 %`).

#### Minimal smoke test

To confirm basic local inference without touching any project repository: 

- Run a small, already‑pulled model (for example `llama3.1:8B`):
    - `ollama run llama3.1:8B "Say: local AI runtime is working"`
- Expected result: The model responds with a sentence acknowledging that the local AI runtime is working, indicating that the Ollama service, model loading, and inference path are all functioning.

#### Notes and quirks

- Ollama runs as a systemd service (`ollama.service`) and starts automatically on boot once enabled. 
- All inference is performed locally by Ollama on this machine; no paid cloud APIs or external AI endpoints are required for this setup. 
- GPU utilization can be monitored with `nvidia-smi` while running models to observe VRAM usage and confirm that inference is using the RTX 4070. 
