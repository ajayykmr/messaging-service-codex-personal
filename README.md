# Messaging Microservice (MVP)

Implementation of the messaging worker service following the finalized design document.

## Project structure

Directory layout follows the technical specification and separates configuration, adapters, providers, worker engine, and auxiliary tooling.

Refer to the `internal` packages for service logic and the `cmd/<channel>-worker` entrypoints for each channel-specific worker binary.
