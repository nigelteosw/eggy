// A drawn image as the panel serves it, and what the owner can do with one:
// save it, copy it, or hand it to the device's share sheet. The chat and the
// traces view show the same images, so both go through these.

export function imageURL(id: string): string {
  return `/api/images/${id}`;
}

const EXTENSIONS: Record<string, string> = {
  "image/png": "png",
  "image/jpeg": "jpg",
  "image/webp": "webp",
  "image/gif": "gif",
};

// The file name a saved image gets: short, and says it came from Eggy.
export function savedImageName(id: string, mediaType: string): string {
  return `eggy-${id.slice(0, 8)}.${EXTENSIONS[mediaType] ?? "png"}`;
}

async function imageBlob(id: string): Promise<Blob> {
  const response = await fetch(imageURL(id), { credentials: "same-origin" });
  if (!response.ok) throw new Error("This image is no longer kept.");
  return response.blob();
}

// Saves through a download rather than a link to the image, which a browser
// would open in place of the chat.
export async function saveImage(id: string): Promise<void> {
  const blob = await imageBlob(id);
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = savedImageName(id, blob.type);
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// The clipboard reliably takes only PNG, so anything else is redrawn as one.
async function asPNG(blob: Blob): Promise<Blob> {
  if (blob.type === "image/png") return blob;
  const bitmap = await createImageBitmap(blob);
  const canvas = document.createElement("canvas");
  canvas.width = bitmap.width;
  canvas.height = bitmap.height;
  canvas.getContext("2d")?.drawImage(bitmap, 0, 0);
  return new Promise((resolve, reject) =>
    canvas.toBlob((png) => (png ? resolve(png) : reject(new Error("Could not copy this image."))), "image/png"),
  );
}

export function canCopyImages(): boolean {
  return typeof ClipboardItem !== "undefined" && typeof navigator !== "undefined" && !!navigator.clipboard?.write;
}

export async function copyImage(id: string): Promise<void> {
  // Safari wants the item built synchronously inside the gesture, with the
  // blob still pending, so the promise is handed over rather than awaited.
  await navigator.clipboard.write([new ClipboardItem({ "image/png": imageBlob(id).then(asPNG) })]);
}

// A phone's share sheet is where "Save to Photos" lives; a desktop browser
// usually has no file sharing at all, and the button is not shown there.
export function canShareImages(): boolean {
  if (typeof navigator === "undefined" || !navigator.canShare) return false;
  return navigator.canShare({ files: [new File([], "eggy.png", { type: "image/png" })] });
}

export async function shareImage(id: string): Promise<void> {
  const blob = await imageBlob(id);
  const file = new File([blob], savedImageName(id, blob.type), { type: blob.type });
  try {
    await navigator.share({ files: [file] });
  } catch (error) {
    // Dismissing the sheet is a choice, not a failure.
    if (error instanceof DOMException && error.name === "AbortError") return;
    throw error;
  }
}
