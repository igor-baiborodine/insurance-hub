<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Python Rules](#python-rules)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Python Rules

Python is an auxiliary language in Insurance Hub: use it for administrative automation,
deployment helpers, structured-data validation, test harnesses, and repository maintenance when
shell scripting is no longer a clear, reliable fit. It is not a new business-service language;
preserve the Java-to-Go migration direction and existing service boundaries.

For Python tooling implementation or review, read both canonical rules:

- [Python development](python-development.md): language selection, ownership, Make interface,
  implementation, and administrative operations.
- [Python validation](python-validation.md): behavior tests, isolated integration checks, tooling,
  and evidence.

For mixed-language work, also load the rules for the code being changed. A Python helper for a Go
service does not replace the [Go rules](go-rules.md) when Go implementation or tooling is affected.
These rules supplement root policy and add no separate instruction precedence.
