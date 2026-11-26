# Changelog

All notable changes to this project are written here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.2.0] - 2026-11-26
### Added
- Kyverno section in the left rail: policies, exceptions, cleanup policies and policy reports, so Kyverno no longer hides under Custom Resources. Reports with failures or errors show red.

## [0.1.0] - 2026-11-25

First version of Kendra: a read-only console for kubeconfig clusters with Carvel kapp and Istio views.

### Added
- Fleet pages: dashboard, versions, deprecated APIs.
- Insights pages: certificates, image inventory, usage vs requests, security, cloud identities, add-ons, change timeline, compare clusters.
- Topology pages: cluster, zones and pod spread, namespace, Helm, Kapp, Istio. Gateway API page.
- Why?, Related and Impact tabs on resources. Time machine and sparklines from an in-memory history.
- Searchable cluster picker with environment and cloud groups, pins, saved views, CSV and Markdown export.
- Native macOS window, `install.sh`, a universal macOS `.pkg` and a GitHub Actions pipeline.
