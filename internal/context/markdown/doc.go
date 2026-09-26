// Package markdown stores the owner-facing context documents as Markdown:
// SOUL.md shared at the top of the home, and USER.md, MEMORY.md, and WATCH.md
// per account. Writes are locked, atomic, and capped in size, so a document the
// agent writes into every prompt cannot grow without bound.
package markdown
