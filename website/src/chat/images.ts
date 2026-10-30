import { imageURL } from "../lib/images";

// A reply that carried a drawn image is recorded with a marker naming where
// the image was kept -- the bytes never enter the conversation. The chat turns
// each marker into a Markdown image served by the panel, so a reply shows its
// picture both as it arrives and after a reload.
const IMAGE_MARKER = /\[image:([0-9a-f]+)\]/g;

export function withImages(text: string): string {
  return text.replace(IMAGE_MARKER, (_, id: string) => `![Generated image](${imageURL(id)})`);
}

// The images a reply drew, in order, so the viewer can step between them.
export function imageIdsIn(text: string): string[] {
  return Array.from(text.matchAll(IMAGE_MARKER), (match) => match[1]);
}

// The status line the server sends while generate_image runs. It is the
// label in internal/bootstrap/turn_presenter.go; a test there pins it.
export const DRAWING_STEP = "Drawing an image";

// The chat draws its placeholder only while drawing is what the turn is doing
// now, not merely something it did earlier in the turn.
export function isDrawing(steps: string[]): boolean {
  return steps[steps.length - 1] === DRAWING_STEP;
}
