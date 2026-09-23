// Package localprocess runs commands as child processes under one root
// directory, with an environment allowlist, a timeout, capped output, and the
// whole process group killed on exit. It confines paths and resources; it is
// not a sandbox.
package localprocess
