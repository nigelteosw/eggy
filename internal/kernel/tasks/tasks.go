package tasks

import "time"

type Status string

const (
	Pending     Status = "pending"
	Running     Status = "running"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Interrupted Status = "interrupted"
	Cancelled   Status = "cancelled"
)

type Task struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Status        Status    `json:"status"`
	CorrelationID string    `json:"correlation_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Error         string    `json:"error,omitempty"`
}
