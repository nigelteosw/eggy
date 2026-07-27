package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nigelteosw/eggy/plugins/auth/authfile"
)

// MigrateLegacyOAuthRecords folds Eggy's former per-server OAuth files --
// <home>/mcp/<server>/oauth.json -- into the shared auth.json document, then
// removes the tree they lived in.
//
// Records move as opaque ciphertext: nothing here needs the encryption key,
// and a record already present in auth.json is left alone, so re-running
// this after a partial move is safe. A record already in auth.json wins
// because it is the one the running Eggy has been writing.
func MigrateLegacyOAuthRecords(legacyDir, authPath string) error {
	entries, err := os.ReadDir(legacyDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy MCP directory: %w", err)
	}
	file := authfile.Open(authPath)
	for _, entry := range entries {
		if !entry.IsDir() || !oauthServerNamePattern.MatchString(entry.Name()) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(legacyDir, entry.Name(), "oauth.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read legacy MCP OAuth record: %w", err)
		}
		if !json.Valid(body) {
			continue
		}
		if _, err := file.Read(oauthSection, entry.Name()); err == nil {
			continue
		} else if !errors.Is(err, authfile.ErrNotFound) {
			return err
		}
		if err := file.Write(oauthSection, entry.Name(), json.RawMessage(body)); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(legacyDir); err != nil {
		return fmt.Errorf("remove legacy MCP directory: %w", err)
	}
	return nil
}
