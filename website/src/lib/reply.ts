import type { Quote } from "../chat/Composer";

const MAX_QUOTE = 600;

// replyQuote is what the composer quotes when the owner replies to a message.
// Long messages are cut at a sentence-sized length, with an ellipsis so the cut
// is visible.
export function replyQuote(body: string, ownMessage: boolean): Quote {
  const text = body.trim();
  return { text: text.length > MAX_QUOTE ? `${text.slice(0, MAX_QUOTE).trimEnd()}…` : text, ownMessage };
}
