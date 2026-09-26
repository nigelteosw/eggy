package connections

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/auth/grants"
)

type memoryRecords map[string]json.RawMessage

func (m memoryRecords) Read(section, key string) (json.RawMessage, error) {
	record, ok := m[section+"/"+key]
	if !ok {
		return nil, grants.ErrNotFound
	}
	return record, nil
}
func (m memoryRecords) Write(section, key string, record json.RawMessage) error {
	m[section+"/"+key] = record
	return nil
}
func (m memoryRecords) Update(section, key string, mutate func(json.RawMessage) (json.RawMessage, error)) error {
	updated, err := mutate(m[section+"/"+key])
	if err != nil {
		return err
	}
	if updated == nil {
		delete(m, section+"/"+key)
		return nil
	}
	m[section+"/"+key] = updated
	return nil
}
func (m memoryRecords) Delete(section, key string) error {
	delete(m, section+"/"+key)
	return nil
}

func testSealer(t *testing.T) *grants.Sealer {
	t.Helper()
	sealer, err := grants.NewSealer("connections", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	return sealer
}

func TestCredentialsAreSealedMergedAndRedactable(t *testing.T) {
	records := memoryRecords{}
	store := New(records, testSealer(t))
	if got, err := store.Read("discord"); err != nil || len(got) != 0 {
		t.Fatalf("empty read=%v err=%v", got, err)
	}
	if err := store.Write("discord", Credentials{"bot_token": "secret-token"}); err != nil {
		t.Fatal(err)
	}
	if raw := string(records["connections/discord"]); strings.Contains(raw, "secret-token") {
		t.Fatal("token stored in the clear")
	}
	if err := store.Write("discord", Credentials{"public_key": "pk", "bot_token": ""}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read("discord")
	if err != nil || got["public_key"] != "pk" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if _, still := got["bot_token"]; still {
		t.Fatal("a blank value did not clear its field")
	}
	if err := store.Write("whatsapp", Credentials{"session": "s"}); err != nil {
		t.Fatal(err)
	}
	if values := store.Values("discord", "whatsapp", "missing"); len(values) != 2 || values[0] != "pk" || values[1] != "s" {
		t.Fatalf("values=%v", values)
	}
	if err := store.Write("discord", Credentials{"public_key": ""}); err != nil {
		t.Fatal(err)
	}
	if _, present := records["connections/discord"]; present {
		t.Fatal("an emptied record was not deleted")
	}
	if err := store.Write("Bad Id", Credentials{"x": "y"}); err == nil {
		t.Fatal("invalid connection id accepted")
	}
}

func TestRecordMovedToAnotherConnectionDoesNotOpen(t *testing.T) {
	records := memoryRecords{}
	store := New(records, testSealer(t))
	if err := store.Write("discord", Credentials{"bot_token": "t"}); err != nil {
		t.Fatal(err)
	}
	records["connections/whatsapp"] = records["connections/discord"]
	if _, err := store.Read("whatsapp"); err == nil {
		t.Fatal("a record sealed for discord opened as whatsapp")
	}
}

func TestStoreWithoutAKeyReadsEmptyAndRefusesWrites(t *testing.T) {
	var store *Store
	if got, err := store.Read("discord"); err != nil || len(got) != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if err := store.Write("discord", Credentials{"bot_token": "t"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
}
