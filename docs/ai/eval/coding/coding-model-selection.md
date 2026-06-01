> This document details the selection process for local coding models used in this repository on a laptop running Ollama. The target system, equipped with 64 GB of RAM, an Intel i7-14650HX, and an NVIDIA GeForce RTX 4070 Laptop GPU, provides a robust environment for local inference but requires balancing model intelligence against VRAM constraints and generation throughput to maintain a productive developer experience.

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Hardware Context](#hardware-context)
- [Evaluation](#evaluation)
  - [Models](#models)
  - [Prompts](#prompts)
  - [Criteria](#criteria)
- [Performance Summary](#performance-summary)
  - [Qwen2.5-Coder-7B (High-Speed Workhorse)](#qwen25-coder-7b-high-speed-workhorse)
  - [Qwen2.5-Coder-14B (Production Architect)](#qwen25-coder-14b-production-architect)
  - [Codestral-22B (Idiomatic Specialist)](#codestral-22b-idiomatic-specialist)
  - [DeepSeek-Coder-V2-16B (Verbose Assistant)](#deepseek-coder-v2-16b-verbose-assistant)
- [Final Model Selection](#final-model-selection)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Hardware Context

- CPU: Intel i7‑14650HX (16 cores, 24 threads, well-suited for local inference).
- GPU: NVIDIA GeForce RTX 4070 Laptop GPU, 8 GB VRAM.
- RAM: 64 GB.

## Evaluation

### Models

To choose coding models, six candidates were evaluated locally:

- `codestral:22b`
- `deepseek-coder-v2:16b`
- `qwen2.5-coder:7b`
- `qwen2.5-coder:14b`
- `starcoder2:7b`
- `starcoder2:15b`

### Prompts

Each model was tested using the following generic coding prompt suite:

1. **Java: API design and implementation**

“Implement a small Java service component that validates incoming user input, applies a simple
business rule, and returns a structured result object. Use clean class design, include basic error
handling, and keep the code suitable for a typical Spring-style backend, but do not use any external
libraries.”

2. **Java: Robust Error Handling & Thread Safety**

"Design a thread-safe InventoryManager class in Java. Use a ConcurrentHashMap to store item IDs (
String) and their quantities (Integer). Implement a method updateStock(String id, int delta) that
updates the quantity. Ensure the method prevents the stock from going below zero and throws a custom
InsufficientStockException if the delta would result in a negative balance. Use a Java Record for
the Exception and ensure the update is atomic."

3. **Go: Concurrency & Worker Pools**

"Create a Go function 'ProcessRequests(urls []string)' that fetches data from a list of URLs
concurrently. Requirements:

1. Use a worker pool pattern with exactly 3 workers.
2. Use a 'sync.WaitGroup' to wait for all workers to finish.
3. Collect all results (or errors) into a single slice and return it.
4. If a request takes longer than 2 seconds, it should be canceled using 'context.Context'."


4. **Go: Idiomatic Interface Design & Testing**

"Define a 'Storer' interface in Go with 'Save(data string) error' and 'Get(id string) (string,
error)' methods.

1. Implement a 'FileStore' struct that satisfies this interface.
2. Write a function 'ProcessAndStore(s Storer, input string)' that takes the input, converts it to
   JSON, and calls 'Save'.
3. Provide a unit test for 'ProcessAndStore' using a mock implementation of the 'Storer' interface (
   do not use external mocking libraries, write a simple manual mock)."

### Criteria

When recording the results for the above prompts, the following was evaluated:

1. **Syntactic Correctness:** Does the code compile (or would it with standard imports)?
2. **Idiomatic Quality:** Does the Go code use 'camelCase' and proper error checking? Does the Java
   code use modern 11/14/17 patterns?
3. **Safety:** Did the model correctly handle the Java concurrency or the Go context cancellation?
4. **Throughput/Latency:** Record the 'eval rate' (tokens/s) and 'total duration' to see if the 7-8B
   models maintain the 40-50 tokens/s performance you observed in planning.

## Performance Summary

### Qwen2.5-Coder-7B (High-Speed Workhorse)
 - **Role:** Primary "daily driver" for rapid prototyping and boilerplate generation.
 - **Coding Behavior:**
     - Consistently syntactically correct and highly idiomatic.
     - Capable of complex Spring-style architectural splits (Controller/Service) even for simple
       prompts.
     - Occasionally takes logical shortcuts (e.g., using in-memory maps instead of requested
       manual mocks).
 - **Latency and Throughput:**
     - Best-in-class performance, maintaining **~49 tokens/s**.
     - Near-instant prompt prefill (preface processing) at over **2,400 tokens/s**.
 - **When to use:** For iterative development, writing unit tests, and general Java/Go
   implementation where speed is critical.

### Qwen2.5-Coder-14B (Production Architect)
 - **Role:** Deep-reasoning model for safety-critical concurrency and complex patterns.
 - **Coding Behavior:**
     - Exceptional at handling multi-step requirements (e.g., worker pools, atomic state
       transitions).
     - Correctly utilizes modern language features like Java Records and Go `compute` patterns.
     - Most likely to produce production-grade, bug-free code on the first attempt.
 - **Latency and Throughput:**
     - Slower generation at **~13 tokens/s**, resulting in 60s+ wait times for large snippets.
 - **When to use:** When implementing complex concurrency (Java/Go), critical error handling, or
   when instruction-following for specific architectural constraints is paramount.

### Codestral-22B (Idiomatic Specialist)
 - **Role:** Specialized model for high-fidelity idiomatic Go and Java.
     - Exceptional at following "no external library" constraints.
 - **Coding Behavior:**
     - Produces the most realistic system code (e.g., actual disk I/O instead of map-based
       mocks).
     - Very strong at manual mocking and test-driven development.
 - **Latency and Throughput:**
     - Lowest throughput at **~7.5 tokens/s**. Total durations often exceed 90 seconds.
 - **When to use:** For complex interface design and high-level architectural patterns where "
   best practice" code structure is more important than speed.

### DeepSeek-Coder-V2-16B (Verbose Assistant)
 - **Role:** General-purpose coding assistant with high verbosity.
 - **Coding Behavior:**
     - Highly detailed explanations and step-by-step guides.
     - Prone to "hallucinating" dependencies (e.g., adding Spring imports when told not to).
     - Shows slight weaknesses in atomic concurrency logic compared to Qwen.
 - **Latency and Throughput:**
     - Respectable **~35-39 tokens/s**, providing a middle ground between speed and size.
 - **When to use:** When you need thorough explanations and documentation alongside the code.

## Final Model Selection

Based on local hardware benchmarks and qualitative analysis:

 1. **Primary Interactive Model:** `qwen2.5-coder:7b`
     - *Reason:* Unmatched responsiveness (49 tokens/s) and high idiomatic quality on this
       hardware.
 2. **Safety-Critical/Concurrency Model:** `qwen2.5-coder:14b`
     - *Reason:* Superior instruction following for complex Go/Java patterns and thread-safety
       requirements.
 3. **Reference/Idiomatic Backup:** `codestral:22b`
     - *Reason:* Exceptional "clean code" standards for structural reference, despite higher
       latency.

**Note on StarCoder2:** During evaluation, both the 7B and 15B variants struggled with
instruction following and often produced incomplete or non-compiling stubs. They are **not
recommended** for the "Instruct" style workflows used in this project.