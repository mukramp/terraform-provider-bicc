package bicc

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/tra-jkv/terraform-provider-bicc/bicc/client"
)

const defaultPort = 443

var _ provider.Provider = &biccProvider{}

type biccProvider struct{}

type biccProviderModel struct {
	Host     types.String `tfsdk:"host"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	Port     types.Int64  `tfsdk:"port"`
}

func New() provider.Provider {
	return &biccProvider{}
}

func (p *biccProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "bicc"
}

func (p *biccProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Provider for managing Oracle Business Intelligence Cloud Connector (BICC) extraction jobs.",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Optional:    true,
				Description: "The Oracle Fusion Applications hostname (e.g., servername.fa.us2.oraclecloud.com). Can also be set via BICC_HOST environment variable. Required unless the environment variable is set.",
			},
			"username": schema.StringAttribute{
				Optional:    true,
				Description: "Username for BICC authentication. Can also be set via BICC_USERNAME environment variable. Required unless the environment variable is set.",
			},
			"password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Password for BICC authentication. Can also be set via BICC_PASSWORD environment variable. Required unless the environment variable is set.",
			},
			"port": schema.Int64Attribute{
				Optional: true,
				Description: "Port for the BICC API. Defaults to 443 when not set. " +
					"Must be between 1 and 65535.",
			},
		},
	}
}

func (p *biccProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config biccProviderModel
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Config takes precedence over environment variables.
	host := os.Getenv("BICC_HOST")
	if !config.Host.IsNull() && !config.Host.IsUnknown() {
		host = config.Host.ValueString()
	}

	username := os.Getenv("BICC_USERNAME")
	if !config.Username.IsNull() && !config.Username.IsUnknown() {
		username = config.Username.ValueString()
	}

	password := os.Getenv("BICC_PASSWORD")
	if !config.Password.IsNull() && !config.Password.IsUnknown() {
		password = config.Password.ValueString()
	}

	port := int64(defaultPort)
	if !config.Port.IsNull() && !config.Port.IsUnknown() {
		port = config.Port.ValueInt64()
		if port < 1 || port > 65535 {
			resp.Diagnostics.AddError("Invalid port", fmt.Sprintf("port must be between 1 and 65535, got %d.", port))
			return
		}
	}

	if host == "" {
		resp.Diagnostics.AddError("Missing host", "host must be set in provider config or via BICC_HOST environment variable.")
		return
	}
	if username == "" {
		resp.Diagnostics.AddError("Missing username", "username must be set in provider config or via BICC_USERNAME environment variable.")
		return
	}
	if password == "" {
		resp.Diagnostics.AddError("Missing password", "password must be set in provider config or via BICC_PASSWORD environment variable.")
		return
	}

	cfg := &client.Config{
		Host:     host,
		Username: username,
		Password: password,
		Port:     int(port),
	}

	biccClient := client.NewClient(cfg)
	resp.ResourceData = biccClient
	resp.DataSourceData = biccClient
}

func (p *biccProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewBICCJobResource,
	}
}

func (p *biccProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}
