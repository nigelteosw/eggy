// Package google is the Google Workspace tool adapter: Gmail, Calendar, Drive,
// Docs, Sheets, and Contacts, through one shared grant on Eggy's own Workspace
// account.
//
// tools.go builds the tools and classifies every action as a read or a
// mutation for the approval gate; each product has its own file; client.go is
// the HTTP client; oauth.go is the authorization flow, which verifies the
// grant's identity against google.expected_email; and store.go seals the grant.
package google
