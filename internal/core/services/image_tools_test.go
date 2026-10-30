package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

type scriptedImageModel struct {
	response ports.ModelResponse
	request  ports.ModelRequest
}

func (m *scriptedImageModel) Generate(_ context.Context, request ports.ModelRequest) (ports.ModelResponse, error) {
	m.request = request
	return m.response, nil
}

func imageTarget(model ports.Model, output []ports.Modality, usage *[]string) ImageTarget {
	return ImageTarget{
		Alias: "painter", Model: model, ModelID: "vendor/painter",
		Output: func(context.Context) []ports.Modality { return output },
		RecordUsage: func(_ context.Context, alias string, _ ports.ModelUsage) error {
			*usage = append(*usage, alias)
			return nil
		},
	}
}

func TestGenerateImageAsksTheImageModelAndCollectsThePicture(t *testing.T) {
	picture := ports.ContentPart{Type: ports.ModalityImage, MediaType: "image/png", Data: []byte("png")}
	model := &scriptedImageModel{response: ports.ModelResponse{Message: ports.Message{Parts: []ports.ContentPart{picture}}}}
	var usage []string
	tool := NewGenerateImageTool(imageTarget(model, []ports.Modality{ports.ModalityImage}, &usage))
	ctx, drawn := WithGeneratedImages(context.Background())
	output, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"a gold horizon"}`))
	if err != nil {
		t.Fatal(err)
	}
	// An image-only model is asked for images alone, with its own ID.
	if model.request.Model != "vendor/painter" || len(model.request.OutputModalities) != 1 || model.request.OutputModalities[0] != ports.ModalityImage {
		t.Fatalf("request=%+v", model.request)
	}
	if len(drawn.Images()) != 1 || !strings.Contains(string(output), "Drew 1") {
		t.Fatalf("drawn=%d output=%s", len(drawn.Images()), output)
	}
	if len(usage) != 1 || usage[0] != "painter" {
		t.Fatalf("usage booked to %v", usage)
	}
}

func TestGenerateImageRelaysARefusalAndRefusesANonDrawingModel(t *testing.T) {
	model := &scriptedImageModel{response: ports.ModelResponse{Message: ports.Message{Content: "I can't draw that."}}}
	var usage []string
	ctx, drawn := WithGeneratedImages(context.Background())
	output, err := NewGenerateImageTool(imageTarget(model, []ports.Modality{ports.ModalityImage}, &usage)).Execute(ctx, json.RawMessage(`{"prompt":"x"}`))
	if err != nil || !strings.Contains(string(output), "I can't draw that.") || len(drawn.Images()) != 0 {
		t.Fatalf("output=%s err=%v", output, err)
	}
	if _, err := NewGenerateImageTool(imageTarget(model, nil, &usage)).Execute(ctx, json.RawMessage(`{"prompt":"x"}`)); err == nil || !strings.Contains(err.Error(), "Settings") {
		t.Fatalf("non-drawing model err=%v", err)
	}
	// Outside a turn there is nowhere for a picture to go.
	if _, err := NewGenerateImageTool(imageTarget(model, []ports.Modality{ports.ModalityImage}, &usage)).Execute(context.Background(), json.RawMessage(`{"prompt":"x"}`)); err == nil {
		t.Fatal("drew outside a turn")
	}
}
