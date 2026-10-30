// A reply that carried a drawn image is recorded with a marker naming where
// the image was kept -- the bytes never enter the conversation. The chat turns
// each marker into a Markdown image served by the panel, so a reply shows its
// picture both as it arrives and after a reload.
const IMAGE_MARKER = /\[image:([0-9a-f]+)\]/g;

export function withImages(text: string): string {
  return text.replace(IMAGE_MARKER, (_, id: string) => `![Generated image](/api/images/${id})`);
}
