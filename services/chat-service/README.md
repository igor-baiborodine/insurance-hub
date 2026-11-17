<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Chat service](#chat-service)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Chat service

`chat-service` is the standalone Go foundation for Insurance Hub Chat. C01 establishes module
ownership only: the following delivery step adds one business HTTP listener, one management HTTP
listener and their bounded lifecycle. WebSocket room behavior, browser routing and Java retirement
remain outside this module's current scope.

The service uses the published `libs/servicekit` module selected in `go.mod`. It does not use a Go
workspace, a filesystem replacement, another service's private packages or Java runtime code.

Source-copy provenance is recorded in [COPYING.md](COPYING.md). Active service architecture,
configuration, validation, image and deployment guidance is completed with the owned runtime and
tooling in the later C01 delivery steps.
