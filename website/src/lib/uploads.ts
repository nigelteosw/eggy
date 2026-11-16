import type { ChatUpload } from "./api";

// What the chat can send a model: the list internal/ports.ReadableMediaType
// holds every upload to, and the size Telegram also stops at. Checking here
// only saves a round trip; the server checks the bytes again.
export const UPLOAD_TYPES = ["image/jpeg", "image/png", "image/webp", "image/gif", "application/pdf"];
export const MAX_UPLOAD_BYTES = 20 << 20;
export const MAX_UPLOAD_FILES = 10;

// uploadProblem is why a picked file cannot go, or null when it can.
export function uploadProblem(file: { name: string; type: string; size: number }): string | null {
  if (!UPLOAD_TYPES.includes(file.type)) return `${file.name} isn't something Eggy can read yet. Send an image or a PDF.`;
  if (file.size > MAX_UPLOAD_BYTES) return `${file.name} is over the 20 MB limit.`;
  if (file.size === 0) return `${file.name} is empty.`;
  return null;
}

export function encodeUpload(file: File): Promise<ChatUpload> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error(`Could not read ${file.name}`));
    reader.onload = () => {
      const url = String(reader.result);
      resolve({ name: file.name, media_type: file.type, data: url.slice(url.indexOf(",") + 1) });
    };
    reader.readAsDataURL(file);
  });
}

// The conversation record keeps no bytes: turns.durableMessageText writes a
// marker line per file instead, and drops the prompt a message made only of
// files was sent with, since those were never the owner's words.
const IMAGE_MARKER = "[image attached]";
const FILE_MARKER = /^\[file attached: (.*)\]$/;
const PLACEHOLDERS = ["Describe this image.", "Read this file."];

export type Attachment = { kind: "image" | "file"; name: string };

function marker(attachment: Attachment): string {
  return attachment.kind === "image" ? IMAGE_MARKER : `[file attached: ${attachment.name}]`;
}

export function attachmentOf(file: { name: string; type: string }): Attachment {
  return { kind: file.type === "application/pdf" ? "file" : "image", name: file.name };
}

// The prompt the server substitutes for a message with files and no words,
// in the panel's copy of panel.defaultUploadPrompt.
export function uploadPlaceholder(attachments: Attachment[]): string {
  return attachments.some((a) => a.kind === "file") ? PLACEHOLDERS[1] : PLACEHOLDERS[0];
}

// recordedText is what history will hold for a message, so the bubble shown
// on send is dropped when the recorded one arrives. prompt is the text the
// model read, quote prefix included.
export function recordedText(prompt: string, attachments: Attachment[]): string {
  let text = prompt.trim();
  if (attachments.length === 0) return text;
  if (PLACEHOLDERS.includes(text)) text = "";
  return [text, ...attachments.map(marker)].filter(Boolean).join("\n");
}

// splitAttachments takes the marker lines off the end of a recorded message,
// so the transcript draws them as attachments rather than brackets.
export function splitAttachments(text: string): { body: string; attachments: Attachment[] } {
  const lines = text.split("\n");
  const attachments: Attachment[] = [];
  while (lines.length > 0) {
    const last = lines[lines.length - 1];
    const file = FILE_MARKER.exec(last);
    if (last === IMAGE_MARKER) attachments.unshift({ kind: "image", name: "Image" });
    else if (file) attachments.unshift({ kind: "file", name: file[1] });
    else break;
    lines.pop();
  }
  return { body: lines.join("\n"), attachments };
}
