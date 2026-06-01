> This document explains how to choose a language model for local development on a laptop running Ollama or LM Studio. The target system has 64 GiB of RAM, an Intel Core i7-14650HX, an NVIDIA GeForce RTX 4070 Laptop GPU, and Ubuntu 24.04.4 LTS, which is a strong local development setup but still one that requires careful attention to VRAM, quantization, and model size when selecting practical models.

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Local Environment](#local-environment)
- [Hardware-based Decision Logic](#hardware-based-decision-logic)
- [Recommended Model Size Range](#recommended-model-size-range)
- [What to Choose First](#what-to-choose-first)
- [When to Prefer Smaller or Larger Models](#when-to-prefer-smaller-or-larger-models)
- [Why Model Size Matters on This Laptop](#why-model-size-matters-on-this-laptop)
- [Why These Runtime Metrics Matter Locally](#why-these-runtime-metrics-matter-locally)
  - [Time to First Token](#time-to-first-token)
  - [Total Generation Time](#total-generation-time)
  - [Tokens Per Second](#tokens-per-second)
- [Practical Selection Recommendation](#practical-selection-recommendation)
- [Decision Template for This Machine](#decision-template-for-this-machine)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Local Environment

The intended deployment environment is local inference rather than a hosted API. That changes the model-selection logic: beyond answer quality, the decision must account for whether the model fits comfortably on the available GPU, how much system RAM is available when the model spills beyond VRAM, and whether acceptable speed can be achieved through GPU offload in Ollama or LM Studio. [blogs.nvidia](https://blogs.nvidia.com/blog/ai-decoded-lm-studio/)

For this system, the most important hardware constraint is likely the RTX 4070 Laptop GPU’s effective VRAM budget rather than CPU capacity or total system RAM. LM Studio recommends at least 16 GB of RAM and at least 4 GB of dedicated VRAM, while broader local-LLM guidance shows that practical model size is usually limited first by VRAM and only then by system RAM and storage. [github](https://github.com/aleibovici/ollama-gpu-calculator)

## Hardware-based Decision Logic

For local LLM use, VRAM determines whether a model can run mostly on the GPU, partially offloaded to the GPU, or mainly on the CPU. LM Studio guidance notes that smaller GPUs in the 4 GB to 8 GB range generally benefit from partial offload, while NVIDIA explains that 8 GB GPUs can still gain meaningful acceleration on larger quantized models even when the full model does not fit entirely in VRAM. [medium](https://medium.com/@sanjeets1900/running-your-local-llm-with-lm-studio-c504036d4b96)

Because this laptop also has 64 GiB of RAM, it can run models that exceed VRAM by placing part of the workload in system memory. That broadens the range of usable models, but performance usually drops when a model no longer fits well on the GPU, so “can run” and “runs well” should be treated as different thresholds. [shshell](https://shshell.com/blog/ollama-module-1-lesson-4)

## Recommended Model Size Range

For this machine, the most practical default range is usually 7B to 14B for smooth local development, especially when the goal is responsive iteration in chat, coding help, document drafting, or structured extraction. Common hardware guidance for Ollama-sized deployments places 7B to 8B models around 4 GB to 8 GB VRAM and 13B to 14B models around 8 GB to 16 GB VRAM, which aligns well with an RTX 4070 Laptop GPU plus ample system RAM. [github](https://github.com/aleibovici/ollama-gpu-calculator)

A 24B-class model is possible, but it is no longer the obvious default for daily local development on this hardware. LM Studio’s Magistral page says the 24B model needs at least 15 GB of RAM and may require up to 19 GB, and NVIDIA’s explanation of 4-bit quantization shows why larger models often exceed 8 GB VRAM and therefore depend on offloading rather than full-GPU execution on a laptop 4070. [lmstudio](https://lmstudio.ai/models/magistral)

Models above roughly 30B are generally not the best fit for this system if the priority is good developer ergonomics. Broader local guidance places 30B+ models in the 24 GB+ VRAM class for comfortable operation, which means they are more likely to run slowly or with substantial compromise on this laptop even with 64 GiB of system memory available. [shshell](https://shshell.com/blog/ollama-module-1-lesson-4)

## What to Choose First

The best starting point on this system is a quantized 7B, 8B, or 14B model in Ollama or a GGUF model in LM Studio. These sizes usually provide the best balance of answer quality, low setup friction, faster token generation, and a higher chance that enough of the model can stay on the GPU for responsive interaction. [blogs.nvidia](https://blogs.nvidia.com/blog/ai-decoded-lm-studio/)

A 24B model should be treated as a secondary option for targeted use rather than the baseline choice. It makes more sense when the task genuinely benefits from stronger reasoning or broader capability and when slower responses are acceptable. [lmstudio](https://lmstudio.ai/models/magistral)

## When to Prefer Smaller or Larger Models

Smaller models are the better choice when the work is interactive and frequent, such as local coding assistance, shell-command drafting, summarization, classification, extraction, or test prompt iteration. In those cases, responsiveness matters more than squeezing out the last increment of reasoning quality, so models that fit more comfortably within the GPU budget are usually preferable. [medium](https://medium.com/@sanjeets1900/running-your-local-llm-with-lm-studio-c504036d4b96)

Larger local models are worth using when the task is genuinely reasoning-heavy, the prompts are more complex, or the expected outputs are more nuanced. Even then, local deployment should be judged by the actual runtime experience on the machine, not by the model’s reputation or parameter count alone. [blogs.nvidia](https://blogs.nvidia.com/blog/ai-decoded-lm-studio/)

## Why Model Size Matters on This Laptop

In names such as 7B, 14B, or 24B, the “B” means billions of parameters. Larger parameter counts usually imply greater model capacity, but on a local machine they also imply higher memory use and a greater chance that the model must spill out of VRAM into system RAM, which is the point where speed often drops sharply. [github](https://github.com/aleibovici/ollama-gpu-calculator)

That trade-off is especially important on this system because the laptop has strong CPU and RAM resources but not workstation-class GPU memory. As a result, a 14B model may be the best daily driver even if a 24B model is technically runnable, because the smaller model is more likely to deliver better responsiveness for repeated development tasks. [shshell](https://shshell.com/blog/ollama-module-1-lesson-4)

## Why These Runtime Metrics Matter Locally

For local development, runtime metrics are not just benchmarking details; they directly determine whether the model feels usable during iterative work. The most relevant measurements remain time to first token, total generation time, and tokens per second, but they need to be interpreted through the lens of local hardware and offloading behavior. [medium](https://medium.com/@sanjeets1900/running-your-local-llm-with-lm-studio-c504036d4b96)

### Time to First Token

Time to the first token is the delay between submitting the prompt and seeing the first generated token. On a local machine, this metric matters because it reflects startup overhead, model loading behavior, memory placement, and how much of the workload is waiting on slower memory or partial CPU execution. [blogs.nvidia](https://blogs.nvidia.com/blog/ai-decoded-lm-studio/)

For this laptop, TTFT is a critical selection metric because a model that begins answering quickly will feel much better during development, even if its total reasoning quality is somewhat lower. This is especially true for terminal-style workflows, chat iteration, and prompt tuning where many short exchanges occur in sequence. [medium](https://medium.com/@sanjeets1900/running-your-local-llm-with-lm-studio-c504036d4b96)

### Total Generation Time

Total generation time measures how long it takes to finish the entire response. This is important because some locally runnable models may have acceptable startup latency but become tedious on longer outputs if they rely heavily on RAM or CPU after the first tokens begin streaming. [github](https://github.com/aleibovici/ollama-gpu-calculator)

On this hardware, total generation time becomes especially important when generating code, long explanations, or summarized documents. It is the clearest measure of whether a larger model is practically worth the extra quality it may provide. [lmstudio](https://lmstudio.ai/models/magistral)

### Tokens Per Second

Tokens per second measure output speed once generation is underway. In local development, it helps distinguish a model that merely works from a model that is pleasant to use, since an under-accelerated local model can feel sluggish even if it is technically functional. [blogs.nvidia](https://blogs.nvidia.com/blog/ai-decoded-lm-studio/)

This metric is particularly useful when comparing different quantizations or GPU-offload settings in LM Studio and Ollama. If two models have similar quality, the one with better sustained generation speed is usually the better daily local choice. [medium](https://medium.com/@sanjeets1900/running-your-local-llm-with-lm-studio-c504036d4b96)

## Practical Selection Recommendation

For this Ubuntu laptop, the recommended default strategy is to start with a 7B to 14B quantized model, evaluate it in Ollama and, if needed, compare against the GGUF version in LM Studio using partial or higher GPU offload. This size range is the most likely to deliver a good balance of local quality and responsiveness on an RTX 4070 Laptop GPU with 64 GiB RAM. [lmstudio](https://lmstudio.ai/docs/app/system-requirements)

A 24B model is reasonable as an optional second tier for specialized reasoning tasks, but it should be selected only after checking TTFT, total generation time, and tokens per second on real prompts. If those metrics degrade enough to interrupt normal iteration, the smaller model is the better engineering choice even if the larger one looks stronger on paper. [lmstudio](https://lmstudio.ai/models/magistral)

## Decision Template for This Machine

The following template can be used when documenting a local model decision:

- **Environment:** Ubuntu 24.04.4 LTS, Ollama or LM Studio, RTX 4070 Laptop GPU, 64 GiB RAM.
- **Default target size:** 7B to 14B quantized models for daily work. [shshell](https://shshell.com/blog/ollama-module-1-lesson-4)
- **Stretch size:** 24B only if task quality gains justify slower local performance. [lmstudio](https://lmstudio.ai/models/magistral)
- **Primary metric for chat and coding:** Time to first token. [blogs.nvidia](https://blogs.nvidia.com/blog/ai-decoded-lm-studio/)
- **Primary metric for long outputs:** Total generation time. [blogs.nvidia](https://blogs.nvidia.com/blog/ai-decoded-lm-studio/)
- **Primary metric for comfort during streaming:** Tokens per second. [medium](https://medium.com/@sanjeets1900/running-your-local-llm-with-lm-studio-c504036d4b96)
- **Final rule:** Choose the smallest local model that meets the task quality threshold while staying responsive on the actual machine. [github](https://github.com/aleibovici/ollama-gpu-calculator)

Under these conditions, the strongest general reasoning for model choice is not “largest possible model,” but “largest model that remains comfortably responsive on the RTX 4070 Laptop GPU and fits the local workflow in Ollama or LM Studio.” [github](https://github.com/aleibovici/ollama-gpu-calculator)