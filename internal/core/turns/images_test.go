package turns

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/core/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

var drawnImage = ports.ContentPart{Type: ports.ModalityImage, MediaType: "image/png", Data: []byte("png")}

// imageChannel is a surface that can show pictures.
type imageChannel struct {
	fakeChannel
	images []string
}

func (c *imageChannel) DeliverImage(_ context.Context, id string, image ports.ContentPart) error {
	c.images = append(c.images, id+":"+image.MediaType)
	return nil
}

type fakeImageStore struct{ saved []string }

func (s *fakeImageStore) SaveImage(_ context.Context, traceID string, _ ports.ContentPart) (string, error) {
	s.saved = append(s.saved, traceID)
	return "img-1", nil
}
func (s *fakeImageStore) Image(context.Context, string) (ports.ContentPart, bool, error) {
	return ports.ContentPart{}, false, nil
}

func drawingOutput(context.Context, string) []ports.Modality {
	return []ports.Modality{ports.ModalityImage, ports.ModalityText}
}

func imageService(loop *fakeLoop, channel ports.Channel, conversation *fakeConversation, images ports.ImageStore, traced bool) *Service {
	options := Options{
		Registry: &fakeRegistry{}, Conversation: conversation, Context: fakeContextStore{},
		Store: fakeStore{}, Runtime: fakeRuntime{}, Skills: fakeSkills{}, Loop: loop,
		Channel: channel, Now: func() time.Time { return time.Unix(0, 0).UTC() },
		ImageOutput: drawingOutput, Images: images,
	}
	if traced {
		options.Traces = services.NewTraceRecorder(&recordingTraceStore{}, nil, services.TraceOptions{})
	}
	return New(options)
}

// Only a turn the owner is present for may draw: a scheduled or heartbeat turn
// asking an image model for a picture nobody requested would be a cost nobody
// chose.
func TestOnlyOwnerTurnsAskForImageOutput(t *testing.T) {
	loop := &fakeLoop{reply: "ok"}
	service := imageService(loop, &fakeChannel{}, &fakeConversation{}, nil, false)
	if err := service.OwnerMessage(context.Background(), ports.Message{Content: "draw a cat"}, "telegram"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(loop.options.OutputModalities, ports.ModalityImage) {
		t.Fatalf("owner turn modalities=%v", loop.options.OutputModalities)
	}
	if err := service.ScheduledTurn(context.Background(), "morning brief"); err != nil {
		t.Fatal(err)
	}
	if len(loop.options.OutputModalities) != 0 {
		t.Fatalf("scheduled turn modalities=%v, want none", loop.options.OutputModalities)
	}
}

// A drawn image is kept under the turn's trace, shown on the surface, and the
// conversation records a reference to it rather than the bytes -- which is
// what lets the web chat draw it again after a reload.
func TestGeneratedImageIsKeptShownAndReferenced(t *testing.T) {
	loop := &fakeLoop{reply: "Here is your cat.", images: []ports.ContentPart{drawnImage}}
	channel := &imageChannel{}
	conversation := &fakeConversation{}
	store := &fakeImageStore{}
	if err := imageService(loop, channel, conversation, store, true).OwnerMessage(context.Background(), ports.Message{Content: "draw a cat"}, "telegram"); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.saved[0] == "" {
		t.Fatalf("saved under traces %v, want one under the turn's trace", store.saved)
	}
	if len(channel.delivered) != 1 || channel.delivered[0] != "Here is your cat." {
		t.Fatalf("delivered=%#v", channel.delivered)
	}
	if !slices.Equal(channel.images, []string{"img-1:image/png"}) {
		t.Fatalf("images=%v", channel.images)
	}
	reply := conversation.recorded[len(conversation.recorded)-1]
	if len(reply.Parts) != 0 || reply.Content != "Here is your cat.\n\n"+ports.ImageMarker("img-1") {
		t.Fatalf("recorded=%#v", reply)
	}
}

// With tracing off there is no trace to keep an image under. It is still
// shown, and the record says it was not kept rather than pointing at nothing.
func TestUntracedImageIsShownButNotKept(t *testing.T) {
	loop := &fakeLoop{images: []ports.ContentPart{drawnImage}}
	channel := &imageChannel{}
	conversation := &fakeConversation{}
	store := &fakeImageStore{}
	if err := imageService(loop, channel, conversation, store, false).OwnerMessage(context.Background(), ports.Message{Content: "draw"}, "telegram"); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("saved=%v, want nothing without a trace", store.saved)
	}
	if len(channel.delivered) != 0 {
		t.Fatalf("delivered=%#v, want no empty text message before the image", channel.delivered)
	}
	if !slices.Equal(channel.images, []string{":image/png"}) {
		t.Fatalf("images=%v", channel.images)
	}
	if reply := conversation.recorded[len(conversation.recorded)-1]; reply.Content != unkeptImage {
		t.Fatalf("recorded=%q", reply.Content)
	}
}

// A surface that cannot show a picture says one was made, rather than
// answering with nothing at all.
func TestSurfaceWithoutImagesIsToldInWords(t *testing.T) {
	loop := &fakeLoop{images: []ports.ContentPart{drawnImage}}
	channel := &fakeChannel{}
	if err := imageService(loop, channel, &fakeConversation{}, nil, false).OwnerMessage(context.Background(), ports.Message{Content: "draw"}, "telegram"); err != nil {
		t.Fatal(err)
	}
	if len(channel.delivered) != 1 || !strings.Contains(channel.delivered[0], "can't show images") {
		t.Fatalf("delivered=%#v", channel.delivered)
	}
}
