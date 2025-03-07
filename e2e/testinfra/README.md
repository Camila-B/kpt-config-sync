# CI Setup

Config Sync CI jobs run the end-to-end tests against GKE clusters periodically.
Most GCP resources are managed by [Terraform](./terraform/README.md), but the
following resources need to be configured separately:
- Test OCI images on Google Container Registry
- KCC configurations

## Required environment variables

The OCI images, Helm charts and CSR repositories are hosted in the same project
of the CI clusters. KCC configurations and fleet host project can be shared
across multiple clusters in different projects.

Below is a list of environment variables required by the setup scripts:
- **GCP_PROJECT**: the project that hosts the GKE cluster.
- **GCP_CLUSTER**: the GKE cluster name.
- **GCP_ZONE**: the compute zone for the cluster.
- **PROW_PROJECT**: the project that hosts the prow cluster, which triggers the
prow jobs. The default value is `oss-prow-build-kpt-config-sync`.
- **KCC_MANAGED_PROJECT**: the project that is created with the config-connector
addon enabled for KCC test. The default value is `cs-dev-hub`.

## Usage

1. Push private OCI images to Google Container Registry, for example,
    ```bash
    GCP_PROJECT=your-gcp-project-name make push-test-oci-images-private

1. Set up KCC configurations, for example,
    ```bash
    GCP_PROJECT=your-gcp-project-name GCP_CLUSTER=your-cluster-name GCP_ZONE=your-cluster-zone make set-up-kcc-configs
    ```
