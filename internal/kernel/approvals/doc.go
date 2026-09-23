// Package approvals defines the approval record: one protected Action against
// one payload digest, pending until the owner approves or rejects it. It holds
// only the types; the service that raises, decides, and authorizes approvals
// is services.ApprovalService.
package approvals
