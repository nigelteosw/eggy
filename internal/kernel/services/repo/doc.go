// Package repo is Eggy's read-only view of the configured repositories: listing
// them, opening a checkout as a thread's workspace, reading files from it, and
// reading GitHub metadata (issues, reviews, checks). Nothing here writes to a
// repository; see AGENTS.md before changing that.
//
// workspace_sessions.go binds a thread to its checkout and survives restarts;
// primitive_tools.go is read_file; repository_tools.go and
// repository_metadata_tools.go list repositories and read GitHub; and
// status_tool.go is the status tool.
package repo
