# Terraform Provider for Oracle BICC

A Terraform provider for managing Oracle Business Intelligence Cloud Connector (BICC) extraction jobs.

## Features

- Create and manage BICC extraction jobs using REST API
- Support for incremental extraction
- Configurable chunking for large datasets
- Column-level extraction control
- Filter expressions for data extraction

## Background: Oracle BICC

[Business Intelligence Cloud Connector (BICC)](https://docs.oracle.com/en/cloud/saas/applications-common/25d/biacc/index.html) is an Oracle offering to extract data from Oracle Fusion Applications cloud data sources into an external object storage — either OCI (Oracle Cloud Infrastructure) Object Storage or UCM (Universal Content Management).

### Extract Schedules vs Job Schedules

BICC has two types of schedules:

**Extract schedule** — global configuration per object. Each object has a single last-run reference. On the next run, BICC checks when the last extract ran and pulls incremental data from that point. Because the last-run reference is global, you can only have one incremental cursor per object across the entire system.

**Job schedule** — scoped within the job itself. If the same object appears in two different jobs, each job maintains its own independent last-run reference. This can be used to extract data more frequently than BICC's built-in minimum interval (hourly), for example every 10 minutes — though this should be tested carefully as it may add load to the Fusion database and impact operational performance.

This provider manages **job-scoped** configuration (`bicc_job`), giving you fine-grained control over which objects are extracted together and their independent incremental cursors.

## Design: Scope of This Provider

### What this provider manages

This provider is scoped to BICC **job definitions** — what data to extract, which columns, and incremental extraction settings. It interacts with the BICC REST API to create, update, and delete jobs.

### Job execution and scheduling

Job execution is triggered by an external scheduler (e.g., GCP Cloud Scheduler, AWS EventBridge) via the Oracle ESS SOAP API's `submitRequest` operation, passing the OCI bucket as `EXTERNAL_STORAGE_LIST` at call time.

### Why not use Oracle's built-in BICC scheduling?

Oracle provides a [SOAP API](https://docs.oracle.com/en/cloud/saas/applications-common/25d/biacc/soap-api.html) that can schedule and trigger BICC jobs. However, during testing we found it unreliable and subject to a fundamental constraint:

**You cannot set both a recurring schedule frequency and an external OCI bucket destination at the same time.**

- If a job is configured with a **daily frequency** (scheduled type), the BICC API does not allow an external OCI Object Storage bucket to be set. The extract writes to internal UCM storage only.
- If a job is configured with an **external OCI bucket**, it becomes an **instant/on-demand** type — no recurring frequency can be set natively.

This means Oracle's built-in scheduling is not viable when you need extracts delivered to an external OCI bucket on a recurring schedule — which is the primary use case for data lake pipelines.

## Requirements

- Terraform >= 1.0
- Go >= 1.25.5 (for development; see `go.mod`)
- Oracle Fusion Applications instance with BICC enabled

## Prerequisites

For BICC concepts, configuration (external storage, data stores, jobs), and console navigation, refer to the [Oracle BICC documentation](https://docs.oracle.com/en/cloud/saas/applications-common/25d/biacc/index.html).

## Installation

### Building from Source

1. Clone the repository:
```bash
git clone https://github.com/tra-jkv/terraform-provider-bicc.git
cd terraform-provider-bicc
```

2. Build the provider:
```bash
go build -o terraform-provider-bicc
```

3. Install locally for development:
```bash
mkdir -p ~/.terraform.d/plugins/registry.terraform.io/tra-jkv/bicc/3.0.0/darwin_arm64
cp terraform-provider-bicc ~/.terraform.d/plugins/registry.terraform.io/tra-jkv/bicc/3.0.0/darwin_arm64/
```

Note: Adjust the version and the path based on your OS and architecture (darwin_arm64, darwin_amd64, linux_amd64, etc.). The version directory only needs to match the version you pin in `required_providers`.

Alternative without copying files: use a `dev_overrides` block for `tra-jkv/bicc` in `~/.terraformrc` pointing at the directory that holds the built binary.

## Usage

### Provider Configuration

```hcl
terraform {
  required_providers {
    bicc = {
      source = "tra-jkv/bicc"
      version = "~> 3.0"
    }
  }
}

provider "bicc" {
  host     = "servername.fa.us2.oraclecloud.com"
  username = var.bicc_username
  password = var.bicc_password
  port     = 443  # Optional, defaults to 443
}
```

You can also use environment variables:
```bash
export BICC_HOST="servername.fa.us2.oraclecloud.com"
export BICC_USERNAME="your-username"
export BICC_PASSWORD="your-password"
```

### Creating a BICC Job

**Single Data Store Example:**

```hcl
resource "bicc_job" "crm_extract" {
  name        = "CRM_FULL_EXTRACT_JOB"
  description = "Full extract job for CRM Analytics data"

  data_stores = [
    {
      data_store_key             = "CrmAnalyticsAM.PartiesAnalyticsAM.Person"
      filters                    = "__DATASTORE__.CreationDate > '2024-01-01'"
      is_silent_error            = true
      is_effective_date_disabled = false

      columns = [
        {
          name        = "PersonProfileId"
          is_populate = true
        },
        {
          name        = "PartyId"
          is_populate = true
        }
      ]

      column_overrides = []
    }
  ]
}
```

**Multiple Data Stores in One Job (Recommended for Related Data):**

Group related data stores together for better organization. For example, billing orders with headers and lines:

```hcl
resource "bicc_job" "billing_orders" {
  name        = "BillingOrders"
  description = "Complete billing order lifecycle - headers, lines, fulfillment"

  data_stores = [
    # Sales Order Headers
    {
      data_store_key             = "FscmTopModelAM.ScmExtractAM.DooBiccExtractAM.HeaderExtractPVO"
      is_silent_error            = true
      is_effective_date_disabled = false
      use_union_for_incremental  = true
      initial_extract_date       = "2024-01-01"
      auto_populate_all_columns  = true
      columns                    = []

      column_overrides = [
        {
          name                = "LastUpdateDate"
          is_last_update_date = true
        }
      ]
    },
    # Sales Order Lines
    {
      data_store_key             = "FscmTopModelAM.ScmExtractAM.DooBiccExtractAM.LineExtractPVO"
      is_silent_error            = true
      is_effective_date_disabled = false
      use_union_for_incremental  = true
      initial_extract_date       = "2024-01-01"
      auto_populate_all_columns  = true
      columns                    = []

      column_overrides = [
        {
          name                = "LastUpdateDate"
          is_last_update_date = true
        }
      ]
    },
    # Fulfillment Lines
    {
      data_store_key             = "FscmTopModelAM.ScmExtractAM.DooBiccExtractAM.FulfillLineExtractPVO"
      is_silent_error            = true
      is_effective_date_disabled = false
      use_union_for_incremental  = true
      initial_extract_date       = "2024-01-01"
      auto_populate_all_columns  = true
      columns                    = []

      column_overrides = [
        {
          name                = "LastUpdateDate"
          is_last_update_date = true
        }
      ]
    }
  ]
}
```

**Benefits of Multiple Data Stores per Job:**
- Related data extracts together (e.g., headers + lines)
- Single job execution extracts all related data
- Easier management in external schedulers
- Maintains data consistency across related entities

### Creating a Job with Incremental Extraction

```hcl
resource "bicc_job" "supplier_incremental" {
  name        = "SupplierIncrementalExtract"
  description = "Incremental extract for Supplier data"

  data_stores = [
    {
      data_store_key             = "FscmTopModelAM.PrcExtractAM.PozBiccExtractAM.SupplierExtractPVO"
      is_silent_error            = true
      is_effective_date_disabled = false

      # Enable incremental extraction
      use_union_for_incremental = true
      initial_extract_date      = "2025-01-01" # Optional: omit for all historical data

      # Optional: Enable chunking for large datasets
      chunk_type          = "DateSeqIncr" # requires a column with is_creation_date = true
      chunk_date_seq_incr = 7 # Extract in 7-day chunks

      columns          = []
      column_overrides = []
    }
  ]
}
```

### Job Scheduling

As described in the [Design](#design-scope-of-this-provider) section, job execution is triggered externally via the ESS SOAP API. Any scheduler that can make an HTTP request can be used:

- GCP Cloud Scheduler
- AWS EventBridge
- Azure Logic Apps
- Cron jobs

## Resource Reference

For full resource documentation, see the [Terraform Registry docs](https://registry.terraform.io/providers/tra-jkv/bicc/latest/docs) once published, or the `docs/` folder in this repository.


## Development

### Building

```bash
go build -o terraform-provider-bicc
```

### Testing

```bash
go vet ./...
go test ./... -count=1
```

These are the same build and test steps CI runs on pull requests (`.github/workflows/ci.yml`: `go build ./...`, `go test ./... -v -count=1`). There are no acceptance tests yet (`TF_ACC`), and nothing runs against a live BICC instance automatically.

### Trying the provider locally against a BICC instance

Build the provider, point `dev_overrides` at it and run Terraform from a scratch directory with local state. This writes to the BICC instance you configure, so use a non-production instance and throwaway job names. Credentials come from `BICC_HOST`, `BICC_USERNAME` and `BICC_PASSWORD`; the provider block can then be empty (`provider "bicc" {}`). Run `terraform plan` and read it before `terraform apply`; `terraform destroy` removes the jobs the scratch state created.

### Project documentation

Roadmap, known issues, open assumptions and decisions are in `docs/`; start with [AGENTS.md](AGENTS.md). Release process: tag `vX.Y.Z` on `main`; the release workflow runs GoReleaser (signed multi-platform zips for the Terraform Registry).

### Running Examples

> **Warning:** `examples/main.tf` uses the old v1 block syntax (`data_stores { ... }`) and does not validate against the current schema, and it still contains commented-out backfill examples. See [docs/KNOWN-ISSUES.md](docs/KNOWN-ISSUES.md) (KI-10). For working syntax use the examples above in this README or [docs/resources/job.md](docs/resources/job.md).


```bash
cd examples
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your credentials
terraform init
terraform plan
terraform apply
```

## API Documentation

This provider uses:
- **REST API** for job management: [Oracle BICC REST API](https://docs.oracle.com/en/cloud/saas/applications-common/25d/biacc/manage-meta-data-rest-api1-vo-attributes-for-sdm-vo-and-non-sdm.html)

For job execution (handled separately):
- **SOAP API** for job execution: [Oracle BICC SOAP API](https://docs.oracle.com/en/cloud/saas/applications-common/25d/biacc/soap-api.html)

## Limitations

- Job scheduling must be handled externally (e.g., GCP Cloud Scheduler, AWS EventBridge, cron, etc.)
- Backfills (setting a last extract date) are not supported by the BICC REST API and are not managed by Terraform. Do them in the BICC UI. The `bicc_job_backfill` resource was removed in v3.0.0.
- Deleting a `bicc_job` (or removing it from your configuration) deletes the BICC job, including its last-extract-date state. A rename is a delete plus create in practice, see [docs/KNOWN-ISSUES.md](docs/KNOWN-ISSUES.md) (KI-3).
- Importing an existing job (`terraform import`) currently produces a state without data stores, so do not rely on "import, then plan shows no changes" yet (KI-2).
- Other known problems, with evidence, are listed in [docs/KNOWN-ISSUES.md](docs/KNOWN-ISSUES.md). No automated test runs against a live BICC instance, and several BICC API behaviours are unverified; see [docs/ASSUMPTIONS.md](docs/ASSUMPTIONS.md).

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

## License

MIT License

## Author

Tra Nguyen

## Acknowledgments

- Oracle BICC Documentation
- HashiCorp Terraform Plugin Framework
