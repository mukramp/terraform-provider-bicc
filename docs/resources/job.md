---
page_title: "bicc_job Resource - BICC Provider"
description: |-
  Manages an Oracle BICC extraction job.
---

# bicc_job (Resource)

Manages an Oracle BICC extraction job, including its data stores, column configuration, and incremental extraction settings.

## Example Usage

### Basic Job with Auto-Populate Columns (Recommended)

```terraform
resource "bicc_job" "suppliers" {
  name        = "Suppliers"
  description = "Supplier master data - suppliers and sites"

  data_stores = [
    {
      data_store_key             = "FscmTopModelAM.PrcExtractAM.PozBiccExtractAM.SupplierExtractPVO"
      is_silent_error            = true
      is_effective_date_disabled = false
      use_union_for_incremental  = true
      auto_populate_all_columns  = true
      filters                    = null
      initial_extract_date       = null
      chunk_type                 = null
      chunk_date_seq_incr        = 0
      chunk_date_seq_min         = 0
      chunk_pk_seq_incr          = 0
      column_overrides = [{
        name                = "LastUpdateDate"
        is_last_update_date = true
      }]
      columns = []
    },
    {
      data_store_key            = "FscmTopModelAM.PrcExtractAM.PozBiccExtractAM.SupplierSiteExtractPVO"
      is_silent_error           = true
      use_union_for_incremental = true
      auto_populate_all_columns = true
      column_overrides = [{
        name                = "LastUpdateDate"
        is_last_update_date = true
      }]
      columns = []
    },
  ]
}
```

### Job with Manual Column Selection

```terraform
resource "bicc_job" "crm_extract" {
  name        = "CRMExtract"
  description = "CRM person data with selected columns"

  data_stores = [
    {
      data_store_key            = "CrmAnalyticsAM.PartiesAnalyticsAM.Person"
      is_silent_error           = true
      use_union_for_incremental = false
      auto_populate_all_columns = false
      column_overrides          = []
      columns = [
        {
          name                = "PersonProfileId"
          is_populate         = true
          is_last_update_date = false
        },
        {
          name                = "LastUpdateDate"
          is_populate         = true
          is_last_update_date = true
        },
      ]
    },
  ]
}
```

### Job with Incremental Extraction and Date Chunking

```terraform
resource "bicc_job" "inventory" {
  name        = "InventoryValuations"
  description = "Inventory valuations with date-based chunking"

  data_stores = [
    {
      data_store_key            = "FscmTopModelAM.ScmExtractAM.CstBiccExtractAM.CstInventoryValuationExtractPVO"
      is_silent_error           = true
      use_union_for_incremental = true
      auto_populate_all_columns = true
      initial_extract_date      = "2024-01-01"
      chunk_type                = "DateSeqIncr"
      chunk_date_seq_incr       = 30
      chunk_date_seq_min        = 1
      column_overrides = [
        { name = "LastUpdateDate",   is_last_update_date = true },
        { name = "CreationDate",     is_creation_date    = true },
      ]
      columns = []
    },
  ]
}
```

## Upgrading from v1.x

In v2.0, `data_stores`, `columns`, and `column_overrides` changed from block syntax to assignment syntax.

**Before (v1.x):**
```terraform
data_stores {
  data_store_key = "..."
  columns {
    name = "LastUpdateDate"
  }
}
```

**After (v2.x):**
```terraform
data_stores = [
  {
    data_store_key = "..."
    columns = [
      {
        name                = "LastUpdateDate"
        is_populate         = true
        is_last_update_date = true
      },
    ]
  },
]
```

## Importing Existing Jobs

```shell
terraform import bicc_job.example 123456789
```

~> **Known issue:** import currently produces a state with no `data_stores` (the provider drops every data store that is not already in state), so the first `terraform plan` after an import will show the data stores as changes to add. Review that plan before applying. Tracked as KI-2 in the repository's `docs/KNOWN-ISSUES.md`.

After importing, run `terraform plan` and read it carefully.

## Schema

### Required

- `name` (String) - Name of the BICC job.
- `data_stores` (Set of Objects) - Set of data stores to extract (order-independent). At least one is expected; the provider does not enforce this. See [data_stores](#nested-schema-for-data_stores) below.

### Optional

- `description` (String) - Description of the BICC job.

### Read-Only

- `id` (String) - The BICC job ID.

---

### Nested Schema for `data_stores`

#### Required

- `data_store_key` (String) - The unique key for the data store (e.g., `FscmTopModelAM.PrcExtractAM.PozBiccExtractAM.SupplierExtractPVO`).

#### Optional

- `filters` (String) - Filter expression for data extraction (e.g., `__DATASTORE__.CreationDate > '2024-01-01'`). Set to `null` if unused.
- `is_silent_error` (Boolean) - If `true`, continue extraction even if this data store fails. Default: `false`.
- `is_effective_date_disabled` (Boolean) - If `true`, disable effective date filtering. Default: `false`.
- `use_union_for_incremental` (Boolean) - If `true`, enable incremental extraction using the UNION approach. Default: `false`.
- `initial_extract_date` (String) - Initial extract date for incremental extraction. Format: `YYYY-MM-DD`. Set to `null` to extract all historical data on the first run.
- `chunk_type` (String) - Chunking type for large extractions. Use `DateSeqIncr` to chunk by creation date (requires at least one column marked `is_creation_date = true`). Set to `null` if unused.
- `chunk_date_seq_incr` (Number) - Date sequence increment for chunking (e.g., `7` for weekly chunks). Default: `0`.
- `chunk_date_seq_min` (Number) - Minimum date sequence for chunking. Default: `0`.
- `chunk_pk_seq_incr` (Number) - Primary key sequence increment for chunking. Default: `0`.
- `auto_populate_all_columns` (Boolean) - If `true`, automatically fetch and include all available columns from the data store. Use `column_overrides` to mark specific columns (e.g., `LastUpdateDate`) for incremental tracking. Default: `false`. The column lookup happens at apply time; if it fails (for example the data store key does not exist, which BICC reports as HTTP 500), or leaves no columns, the apply fails with an error naming the `data_store_key` and no job is sent.
- `column_overrides` (List of Objects) - Column overrides applied when `auto_populate_all_columns = true`. Set to `[]` if unused. See [column_overrides](#nested-schema-for-data_storescolumn_overrides) below.
- `columns` (Set of Objects) - Manual column configuration. Use when `auto_populate_all_columns = false`. Set to `[]` if unused. See [columns](#nested-schema-for-data_storescolumns) below.

---

### Nested Schema for `data_stores.column_overrides`

Used with `auto_populate_all_columns = true` to override specific column properties. Only specify the fields you want to override — all boolean fields are optional.

#### Required

- `name` (String) - Column name to override.

#### Optional

- `is_last_update_date` (Boolean) - Mark as last update date column. Set to `true` on the relevant date column for incremental extraction to work correctly.
- `is_creation_date` (Boolean) - Mark as creation date column. Required when using `chunk_type = "DateSeqIncr"`.
- `is_populate` (Boolean) - Override whether this column is included in extraction.
- `is_primary_key` (Boolean) - Mark as primary key column.
- `is_effective_start_date` (Boolean) - Mark as effective start date column.
- `is_natural_key` (Boolean) - Mark as natural key column.

---

### Nested Schema for `data_stores.columns`

Used when `auto_populate_all_columns = false` for explicit column selection.

#### Required

- `name` (String) - Column name.

#### Optional

- `is_populate` (Boolean) - Include this column in extraction. Default: `true`.
- `is_primary_key` (Boolean) - Mark as primary key column. Default: `false`.
- `is_last_update_date` (Boolean) - Mark as last update date column. Default: `false`.
- `is_creation_date` (Boolean) - Mark as creation date column. Default: `false`.
- `is_effective_start_date` (Boolean) - Mark as effective start date column. Default: `false`.
- `is_natural_key` (Boolean) - Mark as natural key column. Default: `false`.
