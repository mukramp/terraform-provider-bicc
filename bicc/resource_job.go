package bicc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/tra-jkv/terraform-provider-bicc/bicc/client"
)

var _ resource.Resource = &biccJobResource{}
var _ resource.ResourceWithImportState = &biccJobResource{}
var _ resource.ResourceWithModifyPlan = &biccJobResource{}

type biccJobModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	DataStores  types.Set    `tfsdk:"data_stores"`
}

type dataStoreModel struct {
	DataStoreKey            types.String `tfsdk:"data_store_key"`
	Filters                 types.String `tfsdk:"filters"`
	IsSilentError           types.Bool   `tfsdk:"is_silent_error"`
	IsEffectiveDateDisabled types.Bool   `tfsdk:"is_effective_date_disabled"`
	UseUnionForIncremental  types.Bool   `tfsdk:"use_union_for_incremental"`
	InitialExtractDate      types.String `tfsdk:"initial_extract_date"`
	ChunkType               types.String `tfsdk:"chunk_type"`
	ChunkDateSeqIncr        types.Int64  `tfsdk:"chunk_date_seq_incr"`
	ChunkDateSeqMin         types.Int64  `tfsdk:"chunk_date_seq_min"`
	ChunkPkSeqIncr          types.Int64  `tfsdk:"chunk_pk_seq_incr"`
	AutoPopulateAllColumns  types.Bool   `tfsdk:"auto_populate_all_columns"`
	ColumnOverrides         types.List   `tfsdk:"column_overrides"`
	Columns                 types.Set    `tfsdk:"columns"`
}

type columnOverrideModel struct {
	Name                 types.String `tfsdk:"name"`
	IsPopulate           types.Bool   `tfsdk:"is_populate"`
	IsPrimaryKey         types.Bool   `tfsdk:"is_primary_key"`
	IsLastUpdateDate     types.Bool   `tfsdk:"is_last_update_date"`
	IsCreationDate       types.Bool   `tfsdk:"is_creation_date"`
	IsEffectiveStartDate types.Bool   `tfsdk:"is_effective_start_date"`
	IsNaturalKey         types.Bool   `tfsdk:"is_natural_key"`
}

type columnModel struct {
	Name                 types.String `tfsdk:"name"`
	IsPopulate           types.Bool   `tfsdk:"is_populate"`
	IsPrimaryKey         types.Bool   `tfsdk:"is_primary_key"`
	IsLastUpdateDate     types.Bool   `tfsdk:"is_last_update_date"`
	IsCreationDate       types.Bool   `tfsdk:"is_creation_date"`
	IsEffectiveStartDate types.Bool   `tfsdk:"is_effective_start_date"`
	IsNaturalKey         types.Bool   `tfsdk:"is_natural_key"`
}

var columnOverrideAttrTypes = map[string]attr.Type{
	"name":                    types.StringType,
	"is_populate":             types.BoolType,
	"is_primary_key":          types.BoolType,
	"is_last_update_date":     types.BoolType,
	"is_creation_date":        types.BoolType,
	"is_effective_start_date": types.BoolType,
	"is_natural_key":          types.BoolType,
}

var columnAttrTypes = map[string]attr.Type{
	"name":                    types.StringType,
	"is_populate":             types.BoolType,
	"is_primary_key":          types.BoolType,
	"is_last_update_date":     types.BoolType,
	"is_creation_date":        types.BoolType,
	"is_effective_start_date": types.BoolType,
	"is_natural_key":          types.BoolType,
}

var dataStoreAttrTypes = map[string]attr.Type{
	"data_store_key":             types.StringType,
	"filters":                    types.StringType,
	"is_silent_error":            types.BoolType,
	"is_effective_date_disabled": types.BoolType,
	"use_union_for_incremental":  types.BoolType,
	"initial_extract_date":       types.StringType,
	"chunk_type":                 types.StringType,
	"chunk_date_seq_incr":        types.Int64Type,
	"chunk_date_seq_min":         types.Int64Type,
	"chunk_pk_seq_incr":          types.Int64Type,
	"auto_populate_all_columns":  types.BoolType,
	"column_overrides":           types.ListType{ElemType: types.ObjectType{AttrTypes: columnOverrideAttrTypes}},
	"columns":                    types.SetType{ElemType: types.ObjectType{AttrTypes: columnAttrTypes}},
}

type biccJobResource struct {
	client *client.Client
}

func NewBICCJobResource() resource.Resource {
	return &biccJobResource{}
}

func (r *biccJobResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_job"
}

func (r *biccJobResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	columnAttrs := map[string]schema.Attribute{
		"name":                    schema.StringAttribute{Required: true, Description: "Column name."},
		"is_populate":             schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Include this column in extraction."},
		"is_primary_key":          schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Mark as primary key column."},
		"is_last_update_date":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Mark as last update date column (required for incremental)."},
		"is_creation_date":        schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Mark as creation date column."},
		"is_effective_start_date": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Mark as effective start date column."},
		"is_natural_key":          schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Mark as natural key column."},
	}

	columnOverrideAttrs := map[string]schema.Attribute{
		"name":                    schema.StringAttribute{Required: true, Description: "Column name to override."},
		"is_populate":             schema.BoolAttribute{Optional: true, Description: "Override: include this column in extraction."},
		"is_primary_key":          schema.BoolAttribute{Optional: true, Description: "Override: mark as primary key column."},
		"is_last_update_date":     schema.BoolAttribute{Optional: true, Description: "Override: mark as last update date column (required for incremental)."},
		"is_creation_date":        schema.BoolAttribute{Optional: true, Description: "Override: mark as creation date column."},
		"is_effective_start_date": schema.BoolAttribute{Optional: true, Description: "Override: mark as effective start date column."},
		"is_natural_key":          schema.BoolAttribute{Optional: true, Description: "Override: mark as natural key column."},
	}

	resp.Schema = schema.Schema{
		Description: "Manages a BICC extraction job.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The numeric ID of the BICC job.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the BICC job.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Description of the BICC job.",
			},
			"data_stores": schema.SetNestedAttribute{
				Required:    true,
				Description: "Set of data stores to extract (order-independent, keyed by data_store_key).",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"data_store_key": schema.StringAttribute{
							Required:    true,
							Description: "Unique key for the data store (e.g., CrmAnalyticsAM.PartiesAnalyticsAM.Person).",
						},
						"filters": schema.StringAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Filter expression for data extraction.",
						},
						"is_silent_error": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Continue extraction even if this data store fails.",
						},
						"is_effective_date_disabled": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Disable effective date filtering.",
						},
						"use_union_for_incremental": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Enable incremental extraction using UNION approach.",
						},
						"initial_extract_date": schema.StringAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Initial extract date for incremental extraction (format: YYYY-MM-DD).",
							PlanModifiers: []planmodifier.String{
								normalizeDatePlanModifier{},
							},
						},
						"chunk_type": schema.StringAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Chunking type for large extractions. Use 'DateSeqIncr' to chunk by creation date (requires at least one column marked is_creation_date=true).",
						},
						"chunk_date_seq_incr": schema.Int64Attribute{
							Optional:    true,
							Computed:    true,
							Description: "Date sequence increment for chunking.",
						},
						"chunk_date_seq_min": schema.Int64Attribute{
							Optional:    true,
							Computed:    true,
							Description: "Minimum date sequence for chunking.",
						},
						"chunk_pk_seq_incr": schema.Int64Attribute{
							Optional:    true,
							Computed:    true,
							Description: "Primary key sequence increment for chunking.",
						},
						"auto_populate_all_columns": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Automatically fetch and include all available columns from the data store. When true, define only column_overrides.",
						},
						"column_overrides": schema.ListNestedAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Column configuration overrides (use with auto_populate_all_columns to mark specific columns like LastUpdateDate as incremental tracking).",
							NestedObject: schema.NestedAttributeObject{
								Attributes: columnOverrideAttrs,
							},
						},
						"columns": schema.SetNestedAttribute{
							Optional:    true,
							Computed:    true,
							Description: "Explicit column configuration. Not needed when auto_populate_all_columns is true.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: columnAttrs,
							},
						},
					},
				},
			},
		},
	}
}

func (r *biccJobResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *biccJobResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan biccJobModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	job, diags := r.buildJobFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobResp, err := r.client.CreateOrUpdateJob(ctx, job)
	if err != nil {
		resp.Diagnostics.AddError("Error creating job", err.Error())
		return
	}

	// A PUT answer with an id that is not a positive number cannot be read back (BICC
	// answers 400 "Invalid Job Id" for it, A6) and cannot be tracked: fail before any GET
	// and write no state.
	if jobResp.ID <= 0 {
		resp.Diagnostics.AddError("Error creating job",
			fmt.Sprintf("The PUT returned HTTP success but BICC returned an unusable id %d for the job. "+
				"No state was saved and the job was not read back.", jobResp.ID))
		return
	}

	plan.ID = types.StringValue(strconv.FormatInt(jobResp.ID, 10))

	// Do NOT write any state before the re-read below succeeds. If state held the id and
	// Create then returned an error, Terraform would save it tainted and the next apply
	// would plan DELETE (about 150s on SIT) followed by create. PUT upserts by name, so if
	// the name matched a job created outside Terraform, that DELETE would remove a real
	// job after a transient re-read failure. With no state, the next apply re-sends the
	// same PUT, which converges without a delete (A1 unverified for rename). The cost is
	// an orphan job on BICC until that next apply (KI-5); the error below says so.

	// Re-read from the API so all Computed fields (is_effective_date_disabled,
	// initial_extract_date, columns, etc.) are populated in state. Without this,
	// Optional+Computed fields not set in config remain unknown after Create.
	refreshed, err := r.client.GetJob(ctx, jobResp.ID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading job after create", orphanReadBackDetail(jobResp.ID, err))
		return
	}

	oldByKey, diags := buildOldDataStoreMap(ctx, plan.DataStores)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	newDataStores, diags := r.buildDataStoresFromAPI(ctx, refreshed.DataStores, oldByKey)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.Name = types.StringValue(refreshed.Name)
	plan.Description = types.StringValue(refreshed.Description)
	plan.DataStores = newDataStores

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// parseStateJobID turns the id stored in state into a job id for Read and Delete. It
// accepts only a positive base-10 int64. Anything else (empty, non-numeric, overflow,
// leading space, 0, negative) adds an error diagnostic and returns false BEFORE any HTTP
// call: BICC answers 400 "Invalid Job Id provided" for such ids (A6), which Read would
// read as "job gone" and drop from state, so they must never reach the API (KI-5).
func parseStateJobID(raw string, diags *diag.Diagnostics) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		diags.AddError("Invalid job ID", fmt.Sprintf("State holds job id %q, which is not a number; state left unchanged: %s", raw, err))
		return 0, false
	}
	if id <= 0 {
		diags.AddError("Invalid job ID", fmt.Sprintf("State holds job id %q, which is not a positive number; state left unchanged.", raw))
		return 0, false
	}
	return id, true
}

// orphanRetryAdvice is the shared tail of every Create error raised after BICC accepted the
// PUT: nothing is in state, the job may be on BICC. The evidence for "the job exists" is only
// a 2xx from the PUT, and same-name re-PUT convergence is unverified (ASSUMPTIONS A1), so the
// text says "as far as reported" and tells the operator to check BICC first.
func orphanRetryAdvice(jobID int64) string {
	return fmt.Sprintf("No state was saved. Re-running apply sends the same job again; PUT upserts by name "+
		"as far as reported (A1, unverified), so this should converge, but check BICC for job %d "+
		"or the job name first.", jobID)
}

// orphanReadBackDetail words the Create error when the PUT was acknowledged but the GET
// that follows failed. A typed not-found (the live 400, A6) is reported as "unknown (or not
// visible)", never as verified absence or presence.
func orphanReadBackDetail(jobID int64, err error) string {
	if errors.Is(err, client.ErrNotFound) {
		return fmt.Sprintf("BICC acknowledged the PUT with job id %d, but BICC answered that job %d is unknown "+
			"(or not visible); it may not have been created.\n\n%s", jobID, jobID, orphanRetryAdvice(jobID))
	}
	return fmt.Sprintf("BICC acknowledged the PUT with job id %d, but reading the job back failed: %s\n\n%s",
		jobID, err, orphanRetryAdvice(jobID))
}

func (r *biccJobResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state biccJobModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobID, ok := parseStateJobID(state.ID.ValueString(), &resp.Diagnostics)
	if !ok {
		return
	}

	job, err := r.client.GetJob(ctx, jobID)
	if err != nil {
		// Drop the resource from state ONLY when the API says the job does not exist.
		// Any other failure (5xx, 401/403, network, bad JSON) must not be read as
		// "deleted": removing state would make Terraform plan to recreate the job
		// (KI-5). Leave state untouched and surface the error.
		// "Does not exist" is HTTP 400 {"code":"Bad Request","message":"Invalid Job Id
		// provided"}, OBSERVED on SIT 2026-11-04 (ASSUMPTIONS A6); a bare 404 is NOT that.
		// See client.isJobNotFound. OPEN RISK: the same 400 may also be returned for a job
		// the account cannot see (permissions); unchecked.
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading job", fmt.Sprintf("Could not read job %d; state left unchanged: %s", jobID, err))
		return
	}

	// Preserve config-only fields (auto_populate_all_columns, column_overrides) from state
	// by keying on data_store_key, since the API does not return them.
	oldByKey, diags := buildOldDataStoreMap(ctx, state.DataStores)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	newDataStores, diags := r.buildDataStoresFromAPI(ctx, job.DataStores, oldByKey)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Do NOT overwrite state.Name from job.Name — the BICC API ignores the name field on
	// update and always returns the original job name. Preserving state.Name (which holds
	// the value from config) prevents perpetual drift after a rename.
	state.Description = types.StringValue(job.Description)
	state.DataStores = newDataStores

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *biccJobResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan biccJobModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state biccJobModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID

	job, diags := r.buildJobFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.CreateOrUpdateJob(ctx, job)
	if err != nil {
		resp.Diagnostics.AddError("Error updating job", err.Error())
		return
	}

	// Re-read from the API so state reflects server-normalized values.
	jobID, err := strconv.ParseInt(plan.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid job ID after update", err.Error())
		return
	}

	refreshed, err := r.client.GetJob(ctx, jobID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading job after update", err.Error())
		return
	}

	// Build oldByKey from the plan (= config). Plan values are authoritative for all
	// config-managed fields (auto_populate_all_columns, column_overrides, is_silent_error,
	// use_union_for_incremental). BICC does not reliably round-trip these fields — some PVO
	// types (e.g. BusinessUnitPVO, FinFun PVOs) always return explicit-column mode regardless
	// of what was sent. Using plan as the source of truth prevents permanent state drift.
	oldByKey, diags := buildOldDataStoreMap(ctx, plan.DataStores)

	newDataStores, diags := r.buildDataStoresFromAPI(ctx, refreshed.DataStores, oldByKey)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// BICC API ignores name on update and returns the original name — keep plan (config) as
	// authoritative so state matches config and avoids "inconsistent result" errors.
	// plan.Name already holds the config value; do not overwrite it from refreshed.Name.
	plan.Description = types.StringValue(refreshed.Description)
	plan.DataStores = newDataStores

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *biccJobResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state biccJobModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jobID, ok := parseStateJobID(state.ID.ValueString(), &resp.Diagnostics)
	if !ok {
		return
	}

	// Delete is idempotent because BICC answers HTTP 200 for a repeat DELETE of a missing
	// id (OBSERVED 2026-11-04, ASSUMPTIONS A5). There is no not-found exemption: any
	// non-2xx (including 400 and 404) is an error, because reporting success while the job
	// may still exist on BICC would drop it from Terraform. No retries (ASSUMPTIONS C2).
	// Deletes take about 2.5 minutes on SIT (A10), against the 300s client timeout.
	if err := r.client.DeleteJob(ctx, jobID); err != nil {
		resp.Diagnostics.AddError("Error deleting job", err.Error())
	}
}

func (r *biccJobResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var state biccJobModel
	state.ID = types.StringValue(req.ID)

	jobID, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a numeric job ID.")
		return
	}

	job, err := r.client.GetJob(ctx, jobID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading job during import", err.Error())
		return
	}

	state.Name = types.StringValue(job.Name)
	state.Description = types.StringValue(job.Description)

	newDataStores, diags := r.buildDataStoresFromAPI(ctx, job.DataStores, map[string]dataStoreModel{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.DataStores = newDataStores

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// ModifyPlan suppresses spurious drift caused by two patterns:
//
//  1. Post-import drift: provider writes auto_populate_all_columns=false + full column
//     list into state (no config context at import time). On the next plan, config says
//     auto_populate_all_columns=true + columns=[], producing a diff with no real change.
//     We detect this and rewrite the plan element to match state, suppressing the diff.
//     The correct state is written on the next apply via Read.
//
//  2. BICC API round-trip drift: BICC does not persist auto_populate_all_columns,
//     column_overrides, is_silent_error, or use_union_for_incremental reliably across
//     all PVO types (e.g. BusinessUnitPVO always comes back as explicit-column mode;
//     mixed-module payloads cause some PVOs to be silently ignored). After any apply
//     where these flags drift in state, the next plan would show a spurious change.
//     We suppress this by always using plan (= config) values for these fields and
//     only using state for purely computed/API fields like columns (when not auto-populated).
func (r *biccJobResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var state biccJobModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var plan biccJobModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var stateDS []dataStoreModel
	resp.Diagnostics.Append(state.DataStores.ElementsAs(ctx, &stateDS, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	stateByKey := make(map[string]dataStoreModel, len(stateDS))
	for _, ds := range stateDS {
		stateByKey[ds.DataStoreKey.ValueString()] = ds
	}

	var planDS []dataStoreModel
	resp.Diagnostics.Append(plan.DataStores.ElementsAs(ctx, &planDS, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// ModifyPlan always rewrites plan elements for existing data stores (keyed by
	// data_store_key) to merge state values for Computed fields. This is necessary
	// because Terraform's SetNestedAttribute computes element hashes before ModifyPlan
	// runs, and when Optional+Computed fields have null plan values (framework default
	// when no prior state element matches), they produce a different hash from the state
	// element — causing a spurious remove+add diff. By rewriting all plan elements here
	// we ensure the plan hash always matches the state hash for unchanged elements.
	//
	// Rules for each field:
	// - User-controlled config fields (is_silent_error, use_union_for_incremental,
	//   is_effective_date_disabled, auto_populate_all_columns, column_overrides):
	//   Use planElem value (= config value) UNLESS it is null/unknown, in which case
	//   fall back to state to preserve Computed behaviour.
	// - BICC API-only fields (columns, filters, chunk_*, initial_extract_date):
	//   Use state value when auto_populate is true (columns managed by provider);
	//   use plan value otherwise (user manages explicit column list).
	// - Special suppression: if config wants auto_populate=true but BICC/state
	//   persistently returns auto_populate=false (FinFun PVOs), suppress the diff
	//   by pinning plan to state when nothing else changed.
	newElements := make([]attr.Value, len(planDS))
	anyModified := false
	for i, planElem := range planDS {
		key := planElem.DataStoreKey.ValueString()
		old, hasState := stateByKey[key]

		var obj attr.Value
		var d diag.Diagnostics

		if !hasState {
			// New data store — use plan values as-is.
			obj, d = types.ObjectValue(dataStoreAttrTypes, map[string]attr.Value{
				"data_store_key":             planElem.DataStoreKey,
				"filters":                    planElem.Filters,
				"is_silent_error":            planElem.IsSilentError,
				"is_effective_date_disabled": planElem.IsEffectiveDateDisabled,
				"use_union_for_incremental":  planElem.UseUnionForIncremental,
				"initial_extract_date":       planElem.InitialExtractDate,
				"chunk_type":                 planElem.ChunkType,
				"chunk_date_seq_incr":        planElem.ChunkDateSeqIncr,
				"chunk_date_seq_min":         planElem.ChunkDateSeqMin,
				"chunk_pk_seq_incr":          planElem.ChunkPkSeqIncr,
				"auto_populate_all_columns":  planElem.AutoPopulateAllColumns,
				"column_overrides":           planElem.ColumnOverrides,
				"columns":                    planElem.Columns,
			})
			resp.Diagnostics.Append(d...)
			newElements[i] = obj
			continue
		}

		// Helper: use plan value if not null/unknown, else fall back to state.
		boolVal := func(p, s types.Bool) types.Bool {
			if !p.IsNull() && !p.IsUnknown() {
				return p
			}
			return s
		}
		int64Val := func(p, s types.Int64) types.Int64 {
			if !p.IsNull() && !p.IsUnknown() {
				return p
			}
			return s
		}
		strVal := func(p, s types.String) types.String {
			if !p.IsNull() && !p.IsUnknown() {
				return p
			}
			return s
		}

		isSilentError := boolVal(planElem.IsSilentError, old.IsSilentError)
		isEffectiveDateDisabled := boolVal(planElem.IsEffectiveDateDisabled, old.IsEffectiveDateDisabled)
		useUnion := boolVal(planElem.UseUnionForIncremental, old.UseUnionForIncremental)
		autoPopulate := boolVal(planElem.AutoPopulateAllColumns, old.AutoPopulateAllColumns)
		filters := strVal(planElem.Filters, old.Filters)
		initialExtractDate := strVal(planElem.InitialExtractDate, old.InitialExtractDate)
		chunkType := strVal(planElem.ChunkType, old.ChunkType)
		chunkDateSeqIncr := int64Val(planElem.ChunkDateSeqIncr, old.ChunkDateSeqIncr)
		chunkDateSeqMin := int64Val(planElem.ChunkDateSeqMin, old.ChunkDateSeqMin)
		chunkPkSeqIncr := int64Val(planElem.ChunkPkSeqIncr, old.ChunkPkSeqIncr)

		columnOverrides := planElem.ColumnOverrides
		if columnOverrides.IsNull() || columnOverrides.IsUnknown() {
			columnOverrides = old.ColumnOverrides
		}

		// Special suppression: config wants auto_populate=true but state has auto_populate=false.
		// This happens in two cases:
		//   1. BICC persistently returns explicit-column mode for some PVO types (e.g. FinFun PVOs)
		//      even after a successful apply — state has auto=false, columns=[].
		//   2. Post-apply state corruption where BICC wrote back explicit columns for a PVO
		//      that was configured as auto-populate — state has auto=false, columns=[many].
		// In both cases, suppress by pinning the plan to state (auto=false, carry state columns).
		// The BICC configuration is already correct; this is a provider-state artifact.
		// Suppress auto_populate drift: config wants true but state/BICC persistently
		// returns false (FinFun PVOs, explicit-column-mode PVOs). Suppress regardless
		// of column_overrides changes — we pin auto to state's false and carry state's
		// columns, but still propagate column_overrides updates to the plan.
		autoPopulateStateDrift := autoPopulate.ValueBool() &&
			!old.AutoPopulateAllColumns.ValueBool() &&
			isSetEmpty(planElem.Columns)

		if autoPopulateStateDrift {
			autoPopulate = old.AutoPopulateAllColumns // pin to false (state value)
			columnOverrides = old.ColumnOverrides     // pin column_overrides to state too (hash must match)
		}

		// Columns: use state if auto_populate (columns managed by provider or BICC-internal),
		// else use plan (user manages explicit list). Also handle post-import case where
		// state has explicit columns but plan has empty (auto_populate=true mode).
		columnsVal := planElem.Columns
		if autoPopulate.ValueBool() && isSetEmpty(planElem.Columns) {
			// auto mode: always use empty (columns not tracked in state)
			columnsVal = types.SetValueMust(types.ObjectType{AttrTypes: columnAttrTypes}, []attr.Value{})
		} else if !autoPopulate.ValueBool() && isSetEmpty(planElem.Columns) && !isSetEmpty(old.Columns) {
			// post-import: carry forward explicit state columns
			columnsVal = old.Columns
		}

		obj, d = types.ObjectValue(dataStoreAttrTypes, map[string]attr.Value{
			"data_store_key":             planElem.DataStoreKey,
			"filters":                    filters,
			"is_silent_error":            isSilentError,
			"is_effective_date_disabled": isEffectiveDateDisabled,
			"use_union_for_incremental":  useUnion,
			"initial_extract_date":       initialExtractDate,
			"chunk_type":                 chunkType,
			"chunk_date_seq_incr":        chunkDateSeqIncr,
			"chunk_date_seq_min":         chunkDateSeqMin,
			"chunk_pk_seq_incr":          chunkPkSeqIncr,
			"auto_populate_all_columns":  autoPopulate,
			"column_overrides":           columnOverrides,
			"columns":                    columnsVal,
		})
		resp.Diagnostics.Append(d...)
		newElements[i] = obj
		anyModified = true // always mark modified when hasState so we always rewrite
	}

	if resp.Diagnostics.HasError() || !anyModified {
		return
	}

	newSet, d := types.SetValue(types.ObjectType{AttrTypes: dataStoreAttrTypes}, newElements)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.DataStores = newSet
	resp.Diagnostics.Append(resp.Plan.Set(ctx, plan)...)
}

func isSetEmpty(s types.Set) bool {
	return s.IsNull() || s.IsUnknown() || len(s.Elements()) == 0
}

func (r *biccJobResource) buildJobFromModel(ctx context.Context, model biccJobModel) (*client.Job, diag.Diagnostics) {
	var diags diag.Diagnostics

	job := &client.Job{
		Name:        model.Name.ValueString(),
		Description: model.Description.ValueString(),
		Schedules:   nil,
	}

	var dsModels []dataStoreModel
	diags.Append(model.DataStores.ElementsAs(ctx, &dsModels, false)...)
	if diags.HasError() {
		return nil, diags
	}

	// Prefetch all data store column metadata in parallel for auto_populate data stores.
	// GetDataStoreColumns can take 5-10s per call on some BICC instances; parallelising
	// reduces total latency from O(n*latency) to O(max_latency).
	type colResult struct {
		cols []client.Column
		err  error
	}
	prefetch := make(map[string]colResult, len(dsModels))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ds := range dsModels {
		if ds.AutoPopulateAllColumns.ValueBool() {
			key := ds.DataStoreKey.ValueString()
			wg.Add(1)
			go func(k string) {
				defer wg.Done()
				cols, err := r.client.GetDataStoreColumns(ctx, k)
				mu.Lock()
				prefetch[k] = colResult{cols: cols, err: err}
				mu.Unlock()
			}(key)
		}
	}
	wg.Wait()

	dataStores := make([]client.DataStore, len(dsModels))
	for i, ds := range dsModels {
		var columns []client.Column

		if ds.AutoPopulateAllColumns.ValueBool() {
			result := prefetch[ds.DataStoreKey.ValueString()]
			allCols, err := result.cols, result.err
			if err != nil {
				columns = []client.Column{}
			} else {
				// Use only BICC's default-selected columns (isPopulate=true in meta),
				// matching the BICC UI default selection behaviour. This avoids
				// ORA-01792 on PVOs with >1000 columns (e.g. TransactionLineDistributionPVO
				// has 2468 total but 227 default-selected).
				// Primary key columns are always included regardless of isPopulate in
				// schema metadata — BICC may mark some PKs as isPopulate=false but they
				// are required for joins and deduplication downstream.
				var selected []client.Column
				for _, col := range allCols {
					if col.IsPopulate || col.IsPrimaryKey {
						selected = append(selected, col)
					}
				}
				columns = make([]client.Column, len(selected))
				for j, col := range selected {
					columns[j] = col.ToJobColumn()
				}

				var overrides []columnOverrideModel
				diags.Append(ds.ColumnOverrides.ElementsAs(ctx, &overrides, false)...)
				if diags.HasError() {
					return nil, diags
				}
				overrideMap := make(map[string]columnOverrideModel, len(overrides))
				for _, o := range overrides {
					overrideMap[o.Name.ValueString()] = o
				}
				for j, col := range columns {
					if o, exists := overrideMap[col.Name]; exists {
						if !o.IsPopulate.IsNull() && !o.IsPopulate.IsUnknown() {
							columns[j].IsPopulate = o.IsPopulate.ValueBool()
						}
						if !o.IsPrimaryKey.IsNull() && !o.IsPrimaryKey.IsUnknown() {
							columns[j].IsPrimaryKey = o.IsPrimaryKey.ValueBool()
						}
						if !o.IsLastUpdateDate.IsNull() && !o.IsLastUpdateDate.IsUnknown() {
							columns[j].IsLastUpdateDate = o.IsLastUpdateDate.ValueBool()
						}
						if !o.IsCreationDate.IsNull() && !o.IsCreationDate.IsUnknown() {
							columns[j].IsCreationDate = o.IsCreationDate.ValueBool()
						}
						if !o.IsEffectiveStartDate.IsNull() && !o.IsEffectiveStartDate.IsUnknown() {
							columns[j].IsEffectiveStartDate = o.IsEffectiveStartDate.ValueBool()
						}
						if !o.IsNaturalKey.IsNull() && !o.IsNaturalKey.IsUnknown() {
							columns[j].IsNaturalKey = o.IsNaturalKey.ValueBool()
						}
					}
				}
				// Remove columns deselected via overrides (is_populate = false).
				var kept []client.Column
				for _, col := range columns {
					if col.IsPopulate {
						kept = append(kept, col)
					}
				}
				columns = kept
			}
		} else {
			var colModels []columnModel
			diags.Append(ds.Columns.ElementsAs(ctx, &colModels, false)...)
			if diags.HasError() {
				return nil, diags
			}
			columns = make([]client.Column, len(colModels))
			for j, col := range colModels {
				columns[j] = client.Column{
					Name:                 col.Name.ValueString(),
					IsPopulate:           col.IsPopulate.ValueBool(),
					IsPrimaryKey:         col.IsPrimaryKey.ValueBool(),
					IsLastUpdateDate:     col.IsLastUpdateDate.ValueBool(),
					IsCreationDate:       col.IsCreationDate.ValueBool(),
					IsEffectiveStartDate: col.IsEffectiveStartDate.ValueBool(),
					IsNaturalKey:         col.IsNaturalKey.ValueBool(),
					ColConversion:        nil,
				}
			}
		}

		var initialExtractDate interface{}
		if !ds.InitialExtractDate.IsNull() && !ds.InitialExtractDate.IsUnknown() {
			dateStr := ds.InitialExtractDate.ValueString()
			if dateStr != "" {
				t, err := time.Parse("2006-01-02", strings.Split(dateStr, "T")[0])
				if err == nil {
					initialExtractDate = t.UTC().Format("2006-01-02T15:04:05.000Z")
				}
			}
		}

		// Send nil when chunk_type is unset so the API omits the field.
		var chunkType interface{}
		if !ds.ChunkType.IsNull() && !ds.ChunkType.IsUnknown() && ds.ChunkType.ValueString() != "" {
			chunkType = ds.ChunkType.ValueString()
		}

		dataStores[i] = client.DataStore{
			DataStoreMeta: client.DataStoreMeta{
				DataStoreKey:            ds.DataStoreKey.ValueString(),
				Filters:                 ds.Filters.ValueString(),
				IsSilentError:           ds.IsSilentError.ValueBool(),
				IsEffectiveDateDisabled: ds.IsEffectiveDateDisabled.ValueBool(),
				UseUnionForIncremental:  ds.UseUnionForIncremental.ValueBool(),
				InitialExtractDate:      initialExtractDate,
				ChunkType:               chunkType,
				ChunkDateSeqIncr:        int(ds.ChunkDateSeqIncr.ValueInt64()),
				ChunkDateSeqMin:         int(ds.ChunkDateSeqMin.ValueInt64()),
				ChunkPkSeqIncr:          int(ds.ChunkPkSeqIncr.ValueInt64()),
				Columns:                 columns,
			},
			GroupNumber:       0,
			GroupItemPriority: 0,
		}
	}

	job.DataStores = dataStores
	return job, diags
}

// buildOldDataStoreMap indexes existing state data stores by data_store_key.
func buildOldDataStoreMap(ctx context.Context, set types.Set) (map[string]dataStoreModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	result := make(map[string]dataStoreModel)

	if set.IsNull() || set.IsUnknown() {
		return result, diags
	}

	var dsModels []dataStoreModel
	diags.Append(set.ElementsAs(ctx, &dsModels, false)...)
	if diags.HasError() {
		return result, diags
	}

	for _, ds := range dsModels {
		result[ds.DataStoreKey.ValueString()] = ds
	}
	return result, diags
}

// buildDataStoresFromAPI converts API data stores into a types.Set.
// Config-only fields (auto_populate_all_columns, column_overrides) are carried
// forward from oldByKey since the API does not return them.
func (r *biccJobResource) buildDataStoresFromAPI(ctx context.Context, apiDS []client.DataStore, oldByKey map[string]dataStoreModel) (types.Set, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Only include data stores that are present in the config (oldByKey).
	// The BICC API may silently re-add data stores that were removed — we must
	// filter them out here so state stays in sync with config, not with the API.
	filtered := make([]client.DataStore, 0, len(apiDS))
	for _, ds := range apiDS {
		if _, exists := oldByKey[ds.DataStoreMeta.DataStoreKey]; exists {
			filtered = append(filtered, ds)
		}
	}
	apiDS = filtered

	dsObjects := make([]attr.Value, len(apiDS))

	for i, ds := range apiDS {
		key := ds.DataStoreMeta.DataStoreKey

		autoPopulate := false
		columnOverridesVal := types.ListValueMust(types.ObjectType{AttrTypes: columnOverrideAttrTypes}, []attr.Value{})
		if old, exists := oldByKey[key]; exists {
			autoPopulate = old.AutoPopulateAllColumns.ValueBool()
			columnOverridesVal = old.ColumnOverrides
		}

		initialExtractDate := types.StringNull()
		if ds.DataStoreMeta.InitialExtractDate != nil {
			if dateStr, ok := ds.DataStoreMeta.InitialExtractDate.(string); ok && dateStr != "" {
				initialExtractDate = types.StringValue(strings.Split(dateStr, "T")[0])
			}
		}

		chunkType := types.StringNull()
		if ds.DataStoreMeta.ChunkType != nil {
			if ct, ok := ds.DataStoreMeta.ChunkType.(string); ok && ct != "" {
				chunkType = types.StringValue(ct)
			}
		}
		// Prefer plan/state chunk_type if the API ignored the update (prod BICC
		// silently discards chunkType for certain PVOs).
		if old, exists := oldByKey[key]; exists {
			if !old.ChunkType.IsNull() && !old.ChunkType.IsUnknown() {
				chunkType = old.ChunkType
			}
		}

		// Omit columns from state when auto_populate is on to avoid drift from
		// API-returned column lists that the config doesn't manage explicitly.
		// Also skip building columns when prior state already had an empty column set:
		// this avoids the O(n) types.SetValue hashing cost for large-column PVOs (e.g.
		// AR jobs with 900+ columns) where ModifyPlan will discard the list anyway.
		oldHadColumns := false
		if old, exists := oldByKey[key]; exists {
			oldHadColumns = !isSetEmpty(old.Columns)
		}
		columnsVal := types.SetValueMust(types.ObjectType{AttrTypes: columnAttrTypes}, []attr.Value{})
		if !autoPopulate && oldHadColumns {
			colObjects := make([]attr.Value, len(ds.DataStoreMeta.Columns))
			for j, col := range ds.DataStoreMeta.Columns {
				colObj, d := types.ObjectValue(columnAttrTypes, map[string]attr.Value{
					"name":                    types.StringValue(col.Name),
					"is_populate":             types.BoolValue(col.IsPopulate),
					"is_primary_key":          types.BoolValue(col.IsPrimaryKey),
					"is_last_update_date":     types.BoolValue(col.IsLastUpdateDate),
					"is_creation_date":        types.BoolValue(col.IsCreationDate),
					"is_effective_start_date": types.BoolValue(col.IsEffectiveStartDate),
					"is_natural_key":          types.BoolValue(col.IsNaturalKey),
				})
				diags.Append(d...)
				colObjects[j] = colObj
			}
			var d diag.Diagnostics
			columnsVal, d = types.SetValue(types.ObjectType{AttrTypes: columnAttrTypes}, colObjects)
			diags.Append(d...)
		}

		filtersVal := types.StringNull()
		if ds.DataStoreMeta.Filters != "" {
			filtersVal = types.StringValue(ds.DataStoreMeta.Filters)
		}

		// For boolean flags that the config explicitly manages (silent_error, union),
		// prefer the plan/state value over the raw API value. The API can return stale
		// values if a previous apply used wrong credentials or BICC silently ignored
		// the update (e.g. mixed-module payloads). Trusting the API unconditionally
		// causes permanent drift whenever BICC's stored value lags behind the config.
		isSilentError := ds.DataStoreMeta.IsSilentError
		useUnion := ds.DataStoreMeta.UseUnionForIncremental
		isEffectiveDateDisabled := ds.DataStoreMeta.IsEffectiveDateDisabled
		if old, exists := oldByKey[key]; exists {
			if !old.IsSilentError.IsNull() && !old.IsSilentError.IsUnknown() {
				isSilentError = old.IsSilentError.ValueBool()
			}
			if !old.UseUnionForIncremental.IsNull() && !old.UseUnionForIncremental.IsUnknown() {
				useUnion = old.UseUnionForIncremental.ValueBool()
			}
			if !old.IsEffectiveDateDisabled.IsNull() && !old.IsEffectiveDateDisabled.IsUnknown() {
				isEffectiveDateDisabled = old.IsEffectiveDateDisabled.ValueBool()
			}
		}

		dsObj, d := types.ObjectValue(dataStoreAttrTypes, map[string]attr.Value{
			"data_store_key":             types.StringValue(key),
			"filters":                    filtersVal,
			"is_silent_error":            types.BoolValue(isSilentError),
			"is_effective_date_disabled": types.BoolValue(isEffectiveDateDisabled),
			"use_union_for_incremental":  types.BoolValue(useUnion),
			"initial_extract_date":       initialExtractDate,
			"chunk_type":                 chunkType,
			"chunk_date_seq_incr":        types.Int64Value(int64(ds.DataStoreMeta.ChunkDateSeqIncr)),
			"chunk_date_seq_min":         types.Int64Value(int64(ds.DataStoreMeta.ChunkDateSeqMin)),
			"chunk_pk_seq_incr":          types.Int64Value(int64(ds.DataStoreMeta.ChunkPkSeqIncr)),
			"auto_populate_all_columns":  types.BoolValue(autoPopulate),
			"column_overrides":           columnOverridesVal,
			"columns":                    columnsVal,
		})
		diags.Append(d...)
		dsObjects[i] = dsObj
	}

	result, d := types.SetValue(types.ObjectType{AttrTypes: dataStoreAttrTypes}, dsObjects)
	diags.Append(d...)
	return result, diags
}

// normalizeDatePlanModifier suppresses diffs caused by date format differences
// between config (YYYY-MM-DD) and the API response (ISO 8601).
type normalizeDatePlanModifier struct{}

func (m normalizeDatePlanModifier) Description(_ context.Context) string {
	return "Suppresses diffs caused by date format differences between config (YYYY-MM-DD) and API response (ISO 8601)."
}

func (m normalizeDatePlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m normalizeDatePlanModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() {
		return
	}
	stateNorm := strings.Split(req.StateValue.ValueString(), "T")[0]
	planNorm := strings.Split(req.PlanValue.ValueString(), "T")[0]
	if stateNorm == planNorm {
		resp.PlanValue = req.StateValue
	}
}
