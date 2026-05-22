# ADR 001: Adopt KServe as the inference platform

## Status

Accepted

## Date

2026-05-18

## Context

The platform currently runs separate Python services for:

- Whisper
- LLM
- BERT
- Bayesian models

That approach causes:

- duplicated runtime code
- difficult scaling
- no canary deployment story
- weak platform abstractions

The platform already uses Argo CD, Crossplane, S3, and GitOps, so the serving layer should fit that operating model instead of introducing a separate serving stack.

## Considered Options

### Option 1: MLflow Serving

Pros:

- already familiar
- simple to start

Cons:

- weaker production serving story
- limited runtime support

### Option 2: Kubeflow

Pros:

- full ML stack

Cons:

- too heavy for this platform
- more than we need for inference-only delivery

### Option 3: KServe

Pros:

- Kubernetes native
- autoscaling
- canary rollout support
- model mesh
- runtime extensibility
- inference graph support

Cons:

- more complex than plain MLflow serving

## Decision

Use:

- MLflow for registry
- KServe for inference
- Crossplane for platform abstractions

## Consequences

Positive:

- one inference layer for all model types
- GitOps-based deployment
- reusable serving runtimes
- a clean platform CRD boundary

Negative:

- platform CRDs and compositions need to be defined and maintained
- the initial integration is more complex than a single-purpose serving app

## Result

KServe becomes the serving runtime for the unified AI inference platform, with Crossplane as the platform engine and MLflow + S3 as the model artifact and registry backbone.

