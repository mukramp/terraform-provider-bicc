---
page_title: "BICC Provider"
description: |-
  Use the BICC provider to manage Oracle Business Intelligence Cloud Connector (BICC) extraction jobs.
---

# BICC Provider

The BICC provider manages [Oracle Business Intelligence Cloud Connector (BICC)](https://docs.oracle.com/en/cloud/saas/applications-common/25d/biacc/index.html) extraction jobs via the BICC REST API.

Use this provider to define and version-control your BICC job configurations as code, including data store selection, incremental extraction settings, and column configuration. Backfills are not managed by this provider; do them in the BICC UI.

## Example Usage

```terraform
terraform {
  required_providers {
    bicc = {
      source  = "tra-jkv/bicc"
      version = "~> 3.0"
    }
  }
}

provider "bicc" {
  host     = "yourinstance.fa.us2.oraclecloud.com"
  username = var.bicc_username
  password = var.bicc_password
}
```

## Authentication

The provider authenticates using HTTP Basic Auth against the Oracle Fusion Applications BICC REST API.

Credentials can be supplied via provider arguments or environment variables:

| Argument   | Environment Variable | Description                              |
|------------|----------------------|------------------------------------------|
| `host`     | `BICC_HOST`          | Oracle Fusion Applications hostname      |
| `username` | `BICC_USERNAME`      | BICC username                            |
| `password` | `BICC_PASSWORD`      | BICC password                            |

## Schema

### Optional

`host`, `username` and `password` must each be set either in the provider block or through the matching environment variable. The provider block takes precedence. If neither is set, the provider fails with a "Missing ..." error.

- `host` (String) - The Oracle Fusion Applications hostname (e.g., `yourinstance.fa.us2.oraclecloud.com`). Env: `BICC_HOST`.
- `username` (String) - Username for BICC authentication. Env: `BICC_USERNAME`.
- `password` (String, Sensitive) - Password for BICC authentication. Env: `BICC_PASSWORD`.
- `port` (Number) - Port for the BICC API. Defaults to `443`.
