package dto

import (
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/service/docmodel/doctypes"
	"github.com/thanhenti/bepaylot/internal/types"
)

// DocTypeList is the body of GET /v1/doc-types.
type DocTypeList struct {
	Data []types.DocTypeView `json:"data"`
}

// DocTypeInUse is the body of a 409 when a type still used by knowledge
// bases is deleted without detach=true.
type DocTypeInUse struct {
	ErrorResponse
	KBs []KBRef `json:"kbs"`
}

// KBRef names a knowledge base.
type KBRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// DocTypeKBsRequest is the body of PUT /v1/doc-types/{code}/kbs.
type DocTypeKBsRequest struct {
	KBIDs []uuid.UUID `json:"kb_ids"`
}

// SampleList is the body of GET /v1/doc-types/{code}/samples and of the
// upload answer.
type SampleList struct {
	Data []doctypes.SampleView `json:"data"`
}

// SampleUploaded is the body of POST /v1/doc-types/{code}/samples.
type SampleUploaded struct {
	Data []types.DocTypeSample `json:"data"`
}

// TruthRequest is the body of PUT /v1/doc-types/{code}/samples/{id}/truth.
type TruthRequest struct {
	Values map[string]string `json:"values"`
}

// RunSamplesRequest is the body of POST /v1/doc-types/{code}/samples/run.
type RunSamplesRequest struct {
	// SampleIDs omitted = every sample that is read.
	SampleIDs []uuid.UUID `json:"sample_ids,omitempty"`
	// Version omitted = the version in use.
	Version int `json:"version,omitempty"`
}

// PromptList is the body of GET /v1/doc-types/{code}/prompts.
type PromptList struct {
	Data []doctypes.PromptView `json:"data"`
}

// VersionRequest names a version of the instructions.
type VersionRequest struct {
	Version int `json:"version" example:"2"`
}

// PromptText is the body of GET /v1/doc-types/{code}/prompts/{version}/text.
type PromptText struct {
	Text string `json:"text"`
}

// JobList is the body of GET /v1/doc-types/{code}/jobs.
type JobList struct {
	Data []types.DocTypeJob `json:"data"`
}
