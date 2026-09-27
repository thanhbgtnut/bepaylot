package types

import (
	"time"

	"github.com/google/uuid"
)

// Callback delivery states (§4.7).
const (
	CallbackPending   = "pending"
	CallbackSucceeded = "succeeded"
	CallbackFailed    = "failed"
)

// CallbackEvent returns the event name sent when a document reaches status,
// or "" when status does not trigger a callback.
func CallbackEvent(status string) string {
	switch status {
	case DocCompleted, DocPartial, DocFailed, DocCancelled:
		return "document." + status
	}
	return ""
}

// DocumentCallback is one delivery of a document's completion callback.
type DocumentCallback struct {
	ID             uuid.UUID         `json:"id"`
	DocumentID     uuid.UUID         `json:"document_id"`
	Gen            int               `json:"gen"`
	Run            int               `json:"run"`
	Event          string            `json:"event"`
	URL            string            `json:"url"`
	State          string            `json:"state"`
	Attempts       int               `json:"attempts"`
	MaxAttempts    int               `json:"max_attempts"`
	LastStatusCode *int              `json:"last_status_code,omitempty"`
	LastError      string            `json:"last_error,omitempty"`
	LastAttemptAt  *time.Time        `json:"last_attempt_at,omitempty"`
	NextAttemptAt  *time.Time        `json:"next_attempt_at,omitempty"`
	DeliveredAt    *time.Time        `json:"delivered_at,omitempty"`
	Payload        map[string]any    `json:"payload,omitempty"`
	History        []CallbackAttempt `json:"history,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// CallbackAttempt is one HTTP attempt of a delivery.
type CallbackAttempt struct {
	Attempt    int       `json:"attempt"`
	StatusCode *int      `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	Response   string    `json:"response,omitempty"`
	DurationMs int       `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// CallbackTaskPayload is the payload of document:callback.
type CallbackTaskPayload struct {
	CallbackID uuid.UUID `json:"callback_id"`
	Attempt    int       `json:"attempt"`
}
