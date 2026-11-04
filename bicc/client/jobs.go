package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Column represents a data store column
type Column struct {
	Name                 string      `json:"name"`
	Label                string      `json:"label,omitempty"`     // Only for GET responses
	DataType             string      `json:"dataType,omitempty"`  // Only for GET responses
	Size                 string      `json:"size,omitempty"`      // Only for GET responses
	Precision            string      `json:"precision,omitempty"` // Only for GET responses
	Scale                string      `json:"scale,omitempty"`     // Only for GET responses
	IsPrimaryKey         bool        `json:"isPrimaryKey"`
	IsPopulate           bool        `json:"isPopulate"`
	IsLastUpdateDate     bool        `json:"isLastUpdateDate"`
	IsNaturalKey         bool        `json:"isNaturalKey"`
	IsEffectiveStartDate bool        `json:"isEffectiveStartDate"`
	IsCreationDate       bool        `json:"isCreationDate"`
	ColConversion        interface{} `json:"colConversion"`
}

// ToJobColumn converts a Column to a clean version for job creation (removes metadata fields)
func (c Column) ToJobColumn() Column {
	return Column{
		Name:                 c.Name,
		IsPrimaryKey:         c.IsPrimaryKey,
		IsPopulate:           c.IsPopulate,
		IsLastUpdateDate:     c.IsLastUpdateDate,
		IsNaturalKey:         c.IsNaturalKey,
		IsEffectiveStartDate: c.IsEffectiveStartDate,
		IsCreationDate:       c.IsCreationDate,
		ColConversion:        c.ColConversion,
		// Omit: Label, DataType, Size, Precision, Scale
	}
}

// DataStoreMeta represents metadata for a data store
type DataStoreMeta struct {
	Columns                 []Column    `json:"columns"`
	DataStoreKey            string      `json:"dataStoreKey"`
	Filters                 string      `json:"filters,omitempty"`
	IsEffectiveDateDisabled bool        `json:"isEffectiveDateDisabled"`
	IsFlexDataStore         bool        `json:"isFlexDataStore"`
	IsSilentError           bool        `json:"isSilentError"`
	InitialExtractDate      interface{} `json:"initialExtractDate,omitempty"`
	ChunkType               interface{} `json:"chunkType,omitempty"`
	ChunkDateSeqIncr        int         `json:"chunkDateSeqIncr"`
	ChunkDateSeqMin         int         `json:"chunkDateSeqMin"`
	ChunkPkSeqIncr          int         `json:"chunkPkSeqIncr"`
	UseUnionForIncremental  bool        `json:"useUnionForIncremental"`
}

// DataStore represents a data store in a job
type DataStore struct {
	DataStoreMeta     DataStoreMeta `json:"dataStoreMeta"`
	LastExtractDate   string        `json:"lastExtractDate,omitempty"`
	GroupNumber       int           `json:"groupNumber"`
	GroupItemPriority int           `json:"groupItemPriority"`
}

// Job represents a BICC job
type Job struct {
	DataStores  []DataStore `json:"dataStores"`
	Schedules   interface{} `json:"schedules"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
}

// JobResponse represents the response from creating/updating a job
type JobResponse struct {
	Status  string `json:"status"`
	ID      int64  `json:"-"`  // Handled by custom unmarshaler
	IDStr   string `json:"id"` // API returns string
	Message string `json:"message,omitempty"`
}

// UnmarshalJSON custom unmarshaler to handle id as string or int
func (jr *JobResponse) UnmarshalJSON(data []byte) error {
	type Alias JobResponse
	aux := &struct {
		IDRaw interface{} `json:"id"`
		*Alias
	}{
		Alias: (*Alias)(jr),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Handle id as string or number
	switch v := aux.IDRaw.(type) {
	case string:
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid id string: %v", err)
		}
		jr.ID = id
		jr.IDStr = v
	case float64:
		jr.ID = int64(v)
		jr.IDStr = strconv.FormatInt(int64(v), 10)
	case int64:
		jr.ID = v
		jr.IDStr = strconv.FormatInt(v, 10)
	default:
		return fmt.Errorf("id must be string or number, got %T", v)
	}

	return nil
}

// CreateOrUpdateJob creates or updates a BICC job using the REST API.
func (c *Client) CreateOrUpdateJob(ctx context.Context, job *Job) (*JobResponse, error) {
	path := "/biacm/rest/meta/jobs/"

	resp, err := c.doRequestWithContext(ctx, http.MethodPut, path, job)
	if err != nil {
		return nil, fmt.Errorf("error creating/updating job: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var jobResp JobResponse
	if err := json.Unmarshal(bodyBytes, &jobResp); err != nil {
		return nil, fmt.Errorf("error unmarshaling response: %v", err)
	}

	return &jobResp, nil
}

// GetJob retrieves a BICC job by ID.
//
// It returns a *NotFoundError (see IsNotFound) only when isJobNotFound says the
// response means "this job does not exist". Every other failure (network error,
// 401/403, 5xx, unparseable body) is returned as an ordinary error, so callers
// can tell a missing job from a transient failure.
func (c *Client) GetJob(ctx context.Context, jobID int64) (*Job, error) {
	path := fmt.Sprintf("/biacm/rest/meta/jobs/%d", jobID)

	resp, err := c.doRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("error getting job: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	if isJobNotFound(resp.StatusCode, bodyBytes) {
		return nil, &NotFoundError{JobID: jobID, Method: http.MethodGet, StatusCode: resp.StatusCode}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var job Job
	if err := json.Unmarshal(bodyBytes, &job); err != nil {
		return nil, fmt.Errorf("error unmarshaling response: %v", err)
	}

	return &job, nil
}

// GetDataStoreColumns fetches all available columns for a data store.
func (c *Client) GetDataStoreColumns(ctx context.Context, dataStoreKey string) ([]Column, error) {
	path := fmt.Sprintf("/biacm/rest/meta/datastores/%s", dataStoreKey)

	resp, err := c.doRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("error getting data store columns: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var dataStore struct {
		Columns []Column `json:"columns"`
	}
	if err := json.Unmarshal(bodyBytes, &dataStore); err != nil {
		return nil, fmt.Errorf("error unmarshaling response: %v", err)
	}

	return dataStore.Columns, nil
}

// DeleteJob deletes a BICC job (note: BICC API may not support DELETE, ASSUMPTIONS A5).
//
// A missing job is reported as a *NotFoundError (see IsNotFound); the caller decides
// whether that counts as success. No retries (ASSUMPTIONS C2).
func (c *Client) DeleteJob(ctx context.Context, jobID int64) error {
	path := fmt.Sprintf("/biacm/rest/meta/jobs/%d", jobID)

	resp, err := c.doRequestWithContext(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("error deleting job: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	if isJobNotFound(resp.StatusCode, bodyBytes) {
		return &NotFoundError{JobID: jobID, Method: http.MethodDelete, StatusCode: resp.StatusCode}
	}
	return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
}
