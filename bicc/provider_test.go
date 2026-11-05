package bicc

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// -------------------------------------------------------------------------
// Provider registration
// -------------------------------------------------------------------------

func TestProvider(t *testing.T) {
	p := New()
	if p == nil {
		t.Fatal("Provider should not be nil")
	}

	schemaResp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("Provider schema has errors: %v", schemaResp.Diagnostics)
	}

	resourceNames := make(map[string]bool)
	for _, rf := range p.Resources(context.Background()) {
		r := rf()
		metaResp := &resource.MetadataResponse{}
		r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "bicc"}, metaResp)
		resourceNames[metaResp.TypeName] = true
	}

	for _, want := range []string{"bicc_job"} {
		if !resourceNames[want] {
			t.Errorf("resource %q not registered", want)
		}
	}
}

// Terraform core rejects configs that omit Required attributes before
// Configure runs, so the env var fallback only works if these are Optional.
func TestProviderSchema_CredentialsOptional(t *testing.T) {
	schemaResp := &provider.SchemaResponse{}
	New().Schema(context.Background(), provider.SchemaRequest{}, schemaResp)

	for _, name := range []string{"host", "username", "password"} {
		attr, ok := schemaResp.Schema.Attributes[name]
		if !ok {
			t.Fatalf("attribute %q missing from provider schema", name)
		}
		if attr.IsRequired() {
			t.Errorf("attribute %q must not be Required, otherwise the env var fallback never runs", name)
		}
		if !attr.IsOptional() {
			t.Errorf("attribute %q must be Optional", name)
		}
	}
}

// -------------------------------------------------------------------------
// Provider.Configure helpers
// -------------------------------------------------------------------------

// providerTFType is the tftypes object type matching the provider schema.
var providerTFType = tftypes.Object{
	AttributeTypes: map[string]tftypes.Type{
		"host":     tftypes.String,
		"username": tftypes.String,
		"password": tftypes.String,
		"port":     tftypes.Number,
	},
}

// providerConfigure builds a ConfigureRequest from a raw tftypes map and
// calls Configure, returning the response.
func providerConfigure(t *testing.T, attrs map[string]tftypes.Value) provider.ConfigureResponse {
	t.Helper()

	p := New()
	schemaResp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, schemaResp)

	rawVal := tftypes.NewValue(providerTFType, attrs)
	cfg := tfsdk.Config{
		Raw:    rawVal,
		Schema: schemaResp.Schema,
	}

	var resp provider.ConfigureResponse
	p.Configure(context.Background(), provider.ConfigureRequest{Config: cfg}, &resp)
	return resp
}

func nullProviderAttrs() map[string]tftypes.Value {
	return map[string]tftypes.Value{
		"host":     tftypes.NewValue(tftypes.String, nil),
		"username": tftypes.NewValue(tftypes.String, nil),
		"password": tftypes.NewValue(tftypes.String, nil),
		"port":     tftypes.NewValue(tftypes.Number, nil),
	}
}

func validProviderAttrs() map[string]tftypes.Value {
	return map[string]tftypes.Value{
		"host":     tftypes.NewValue(tftypes.String, "host.example.com"),
		"username": tftypes.NewValue(tftypes.String, "user"),
		"password": tftypes.NewValue(tftypes.String, "pass"),
		"port":     tftypes.NewValue(tftypes.Number, nil),
	}
}

func copyAttrs(src map[string]tftypes.Value) map[string]tftypes.Value {
	dst := make(map[string]tftypes.Value, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// -------------------------------------------------------------------------
// Provider.Configure — port validation
// -------------------------------------------------------------------------

func TestProviderConfigure_PortValidation(t *testing.T) {
	tests := []struct {
		name    string
		port    tftypes.Value
		wantErr bool
	}{
		{"port 0 rejected", tftypes.NewValue(tftypes.Number, 0), true},
		{"port 65536 rejected", tftypes.NewValue(tftypes.Number, 65536), true},
		{"port 1 accepted", tftypes.NewValue(tftypes.Number, 1), false},
		{"port 443 accepted", tftypes.NewValue(tftypes.Number, 443), false},
		{"port 65535 accepted", tftypes.NewValue(tftypes.Number, 65535), false},
		{"port omitted uses default", tftypes.NewValue(tftypes.Number, nil), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attrs := validProviderAttrs()
			attrs["port"] = tc.port
			resp := providerConfigure(t, attrs)
			if tc.wantErr && !resp.Diagnostics.HasError() {
				t.Error("expected error, got none")
			}
			if !tc.wantErr && resp.Diagnostics.HasError() {
				t.Errorf("unexpected error: %v", resp.Diagnostics)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Provider.Configure — missing required fields
// -------------------------------------------------------------------------

func TestProviderConfigure_MissingFields(t *testing.T) {
	tests := []struct {
		name   string
		setenv func(t *testing.T)
		field  string // which attr to null out in config
	}{
		{
			name:   "missing host",
			setenv: func(t *testing.T) { t.Setenv("BICC_HOST", "") },
			field:  "host",
		},
		{
			name:   "missing username",
			setenv: func(t *testing.T) { t.Setenv("BICC_USERNAME", "") },
			field:  "username",
		},
		{
			name:   "missing password",
			setenv: func(t *testing.T) { t.Setenv("BICC_PASSWORD", "") },
			field:  "password",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Ensure all env vars start populated so only the one under test is missing.
			t.Setenv("BICC_HOST", "host.example.com")
			t.Setenv("BICC_USERNAME", "user")
			t.Setenv("BICC_PASSWORD", "pass")
			tc.setenv(t)

			attrs := nullProviderAttrs() // all config attrs null → env vars used
			resp := providerConfigure(t, attrs)
			if !resp.Diagnostics.HasError() {
				t.Errorf("expected error when %s is missing, got none", tc.field)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Provider.Configure — env var fallback
// -------------------------------------------------------------------------

func TestProviderConfigure_EnvVarFallback(t *testing.T) {
	t.Run("all three from env vars succeed", func(t *testing.T) {
		t.Setenv("BICC_HOST", "host.example.com")
		t.Setenv("BICC_USERNAME", "user")
		t.Setenv("BICC_PASSWORD", "pass")

		resp := providerConfigure(t, nullProviderAttrs())
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected error: %v", resp.Diagnostics)
		}
	})

	t.Run("config takes precedence over env var", func(t *testing.T) {
		// Env vars set to invalid values; config provides valid values — should succeed.
		t.Setenv("BICC_HOST", "")
		t.Setenv("BICC_USERNAME", "")
		t.Setenv("BICC_PASSWORD", "")

		resp := providerConfigure(t, validProviderAttrs())
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected error: %v", resp.Diagnostics)
		}
	})
}

// -------------------------------------------------------------------------
// Provider.Configure — ResourceData is set on success
// -------------------------------------------------------------------------

func TestProviderConfigure_ResourceDataSet(t *testing.T) {
	resp := providerConfigure(t, validProviderAttrs())
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}
	if resp.ResourceData == nil {
		t.Error("ResourceData should be set after successful Configure")
	}
	if resp.DataSourceData == nil {
		t.Error("DataSourceData should be set after successful Configure")
	}
}
