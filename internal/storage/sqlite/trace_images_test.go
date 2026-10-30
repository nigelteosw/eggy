package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func startImageTrace(t *testing.T, store *Store, account, id string, started time.Time) {
	t.Helper()
	if err := store.StartTrace(as(account), ports.Trace{ID: id, ConversationID: "thread", Channel: "web", Kind: ports.TraceKindOwner, StartedAt: started}); err != nil {
		t.Fatal(err)
	}
}

var drawn = ports.ContentPart{Type: ports.ModalityImage, MediaType: "image/png", Data: []byte("\x89PNG pixels")}

func TestImageRoundTripsForItsOwnerOnly(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	startImageTrace(t, store, "owner", "trace-1", time.Now().UTC())
	id, err := store.SaveImage(as("owner"), "trace-1", drawn)
	if err != nil || id == "" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	image, found, err := store.Image(as("owner"), id)
	if err != nil || !found || image.MediaType != "image/png" || string(image.Data) != "\x89PNG pixels" || image.Type != ports.ModalityImage {
		t.Fatalf("image=%#v found=%v err=%v", image, found, err)
	}
	// Another account cannot read it, and is told no more than that it is not there.
	if _, found, err := store.Image(as("other"), id); found || err != nil {
		t.Fatalf("another account read it: found=%v err=%v", found, err)
	}
	// Nor can another account file an image under someone else's trace.
	if _, err := store.SaveImage(as("other"), "trace-1", drawn); !errors.Is(err, ports.ErrTraceNotFound) {
		t.Fatalf("err=%v, want ErrTraceNotFound", err)
	}
}

// An image lives exactly as long as the trace of the turn that drew it, so the
// tracing retention setting is the one control over both.
func TestPruningATraceDeletesItsImages(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	base := time.Now().UTC()
	startImageTrace(t, store, "owner", "old", base)
	startImageTrace(t, store, "owner", "new", base.Add(time.Hour))
	oldID, err := store.SaveImage(as("owner"), "old", drawn)
	if err != nil {
		t.Fatal(err)
	}
	newID, err := store.SaveImage(as("owner"), "new", drawn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PruneTraces(as("owner"), 1, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.Image(as("owner"), oldID); found {
		t.Fatal("the pruned trace's image is still served")
	}
	if _, found, _ := store.Image(as("owner"), newID); !found {
		t.Fatal("the kept trace lost its image")
	}
	var rows int
	if err := store.db.QueryRowContext(as("owner"), `SELECT COUNT(*) FROM trace_images`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("image rows = %d, want the pruned one deleted, not just hidden", rows)
	}
}

func TestSaveImageRefusesAMissingTrace(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	if _, err := store.SaveImage(as("owner"), "never-started", drawn); !errors.Is(err, ports.ErrTraceNotFound) {
		t.Fatalf("err=%v, want ErrTraceNotFound", err)
	}
}

// The traces view shows what a turn drew, so a trace's detail names its
// images in the order they were made -- and only the acting account's trace
// is read at all.
func TestTraceDetailNamesItsImages(t *testing.T) {
	t.Parallel()

	store := openTraceStore(t)
	startImageTrace(t, store, "owner", "trace-1", time.Now().UTC())
	startImageTrace(t, store, "owner", "trace-2", time.Now().UTC())
	first, err := store.SaveImage(as("owner"), "trace-1", drawn)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SaveImage(as("owner"), "trace-1", drawn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveImage(as("owner"), "trace-2", drawn); err != nil {
		t.Fatal(err)
	}
	trace, _, found, err := store.Trace(as("owner"), "trace-1")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(trace.Images) != 2 || trace.Images[0] != first || trace.Images[1] != second {
		t.Fatalf("images = %v, want [%s %s]", trace.Images, first, second)
	}
}
