package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
)

var generateImageSchema = json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","minLength":1,"description":"A complete description of the picture: subject, style, composition, colours, and aspect ratio if one was asked for."}},"required":["prompt"],"additionalProperties":false}`)

// ImageTarget is the model generate_image draws with: the configured image
// alias, resolved by bootstrap into the adapter and the ID it serves.
type ImageTarget struct {
	Alias           string
	Model           ports.Model
	ModelID         string
	ProviderRouting json.RawMessage
	// Output is what the model produces when that includes an image, as its
	// provider lists it, and nil when the provider does not say it draws.
	Output func(context.Context) []ports.Modality
	// RecordUsage books the drawing against the image alias, so its cost
	// shows under that model rather than vanishing.
	RecordUsage func(ctx context.Context, alias string, usage ports.ModelUsage) error
}

type generateImageTool struct{ target ImageTarget }

// NewGenerateImageTool lets whichever model a turn runs on draw, by asking
// the configured image model. The pictures do not go back to the calling
// model, which could not see them anyway; they ride on the turn and arrive
// with its reply.
func NewGenerateImageTool(target ImageTarget) ports.Tool {
	return generateImageTool{target: target}
}

func (t generateImageTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "generate_image",
		Description: "Draw a picture with the owner's image model. Call it whenever the owner asks for an image, photo, drawing or artwork, rather than offering a prompt for them to use elsewhere. Photos the owner attached to this message are handed to the image model with the prompt, so to edit one, say what to change and what to keep rather than describing the whole scene. The picture is attached to your reply automatically; do not describe it as a link.",
		Schema:      generateImageSchema,
		// It changes nothing in Eggy or anywhere else: the provider is asked
		// for a picture the way a turn asks it for text. Unprompted turns
		// cannot reach it, since their allowlist does not name it.
		Effect: ports.ReadOnlyTool(),
	}
}

func (t generateImageTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Prompt string `json:"prompt"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	collector := generatedImagesFrom(ctx)
	if collector == nil {
		return nil, errors.New("images can only be drawn in a conversation")
	}
	output := t.target.Output(ctx)
	if len(output) == 0 {
		return nil, fmt.Errorf("the image model %s is not listed as drawing images; choose another in Settings > Models", t.target.Alias)
	}
	response, err := t.target.Model.Generate(ctx, ports.ModelRequest{
		Model:            t.target.ModelID,
		Messages:         []ports.Message{{Role: ports.RoleUser, Content: prompt, Parts: collector.Attached()}},
		ProviderRouting:  t.target.ProviderRouting,
		OutputModalities: output,
	})
	if t.target.RecordUsage != nil {
		if usageErr := t.target.RecordUsage(ctx, t.target.Alias, response.Usage); usageErr != nil && err == nil {
			err = usageErr
		}
	}
	if err != nil {
		return nil, fmt.Errorf("image model %s: %w", t.target.Alias, err)
	}
	drawn := 0
	for _, part := range response.Message.Parts {
		if part.Type == ports.ModalityImage {
			collector.add(part)
			drawn++
		}
	}
	if drawn == 0 {
		// A refusal or a link comes back as text; the calling model relays it.
		return json.Marshal(map[string]string{"result": "The image model drew nothing.", "image_model_said": strings.TrimSpace(response.Message.Content)})
	}
	return json.Marshal(map[string]string{"result": fmt.Sprintf("Drew %d image(s). They are attached to your reply; say what you made in a sentence or two.", drawn)})
}

// GeneratedImages gathers what generate_image drew during one turn, so the
// turn can deliver and keep the pictures with its reply. It also holds the
// photos the owner attached to the turn: without them an edit is a fresh
// drawing from a description of a photo the image model never saw.
type GeneratedImages struct {
	mu       sync.Mutex
	images   []ports.ContentPart
	attached []ports.ContentPart
}

func (g *GeneratedImages) add(part ports.ContentPart) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.images = append(g.images, part)
}

// Images returns everything drawn so far.
func (g *GeneratedImages) Images() []ports.ContentPart {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ports.ContentPart(nil), g.images...)
}

type generatedImagesKey struct{}

// Attached returns the photos the owner sent with the turn.
func (g *GeneratedImages) Attached() []ports.ContentPart {
	return append([]ports.ContentPart(nil), g.attached...)
}

// WithGeneratedImages opens a turn's image collection, given the parts the
// owner attached; only the images among them reach the image model.
func WithGeneratedImages(ctx context.Context, attached []ports.ContentPart) (context.Context, *GeneratedImages) {
	collector := &GeneratedImages{}
	for _, part := range attached {
		if part.Type == ports.ModalityImage {
			collector.attached = append(collector.attached, part)
		}
	}
	return context.WithValue(ctx, generatedImagesKey{}, collector), collector
}

func generatedImagesFrom(ctx context.Context) *GeneratedImages {
	collector, _ := ctx.Value(generatedImagesKey{}).(*GeneratedImages)
	return collector
}
