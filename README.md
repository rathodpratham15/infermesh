# InferMesh

A Go inference gateway that abstracts multiple LLM serving engines (vLLM, NVIDIA Triton + TensorRT-LLM), with routing, concurrency control, OpenTelemetry/Prometheus observability, Kafka telemetry, Kubernetes/Helm deployment and a reproducible benchmark suite.

> **Status:** under construction. This README describes the project setup only; features are added phase by phase through pull requests, and nothing is documented here until it has been built and tested.

## Hardware

Local development runs on Apple Silicon with no NVIDIA GPU. NVIDIA-specific components (Triton, TensorRT-LLM) are optional deployment profiles that need an NVIDIA GPU machine.
