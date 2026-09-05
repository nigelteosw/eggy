# Telegram image input design

## Problem

Eggy's Telegram webhook decodes only `message.text`. Telegram puts the text
sent with a photo or document in `message.caption`, and represents the media by
a file ID. The webhook therefore turns an image-and-caption update into an
empty text turn and discards the image before it reaches the model. Independently,
Eggy's provider-neutral message and OpenAI-compatible adapter currently encode
message content as a string, so no later layer can carry an image.

The first release will accept one Telegram image sent either as a compressed
photo or as an image document, alongside an optional caption. The original
pixels are available to the active model turn only. Recent conversation history
will retain the caption and a textual attachment marker, not the bytes.

## Goals and non-goals

Goals:

- A Telegram photo with a caption reaches a vision-capable model as text plus
  image content in one user message.
- A Telegram image sent as a document behaves the same way without losing its
  original bytes.
- An image without a caption receives a small neutral prompt so it remains a
  meaningful owner turn.
- The representation between the channel and model is provider-neutral, so a
  future web upload can enter the same path.
- Text-only messages retain their current wire format and behavior.

Non-goals:

- No web upload UI or web endpoint in this change.
- No PDFs, audio, video, albums, outbound media, OCR fallback, image resizing,
  or model-capability registry.
- No durable image recall. A later question cannot inspect old pixels unless
  the owner sends the image again.
- No new configuration keys, durable file tree, background cleanup loop, or
  provider-hosted file upload.

## Design

### Provider-neutral message parts

Extend the existing `ports.Message` rather than introduce another message
abstraction. It gains an optional slice of typed content parts. The first
supported non-text part is an image containing a media type and bytes. Existing
`Content string` remains canonical for text, tool messages, stored history,
prompt construction, tracing, and all existing callers.

The type must be deliberately additive: a later durable attachment design can
add an opaque attachment ID beside the inline bytes. Channel adapters will not
need to change when a resolver learns to load that ID from SQLite. The initial
change will not add an unused ID field or a resolver interface.

`internal/kernel/events.Message` gains the same provider-neutral image payload
shape needed to cross the event boundary. It must not contain Telegram file IDs
or Telegram-specific types. The bootstrap event handler passes the complete
message to the turn service rather than extracting only `Text`.

### Telegram ingestion

The Telegram update decoder adds `caption`, the photo-size array, and document
metadata. For a compressed photo it selects the largest advertised size. For a
document it accepts only the image media types supported by the
OpenAI-compatible image input: JPEG, PNG, WebP, and GIF.

The existing Telegram client gains file retrieval using the same token, base
URL, HTTP client, context, and response-envelope handling as its other Bot API
calls:

1. Call `getFile` with the selected file ID.
2. Download the returned file path from Telegram's file endpoint.
3. Bound the response to Telegram's documented 20 MB download maximum and
   reject a declared or streamed payload above that limit.
4. Detect the downloaded content type and accept only the four image formats.

The webhook depends on a narrow Telegram-local downloader interface implemented
by `Client`; fake implementations keep handler tests independent of HTTP. The
handler copies `caption` into message text and attaches the downloaded image.
An image without a caption uses `Describe this image.` as its current-turn text
and stores `[image attached]` as its history representation.

Malformed, unsupported, oversized, or failed downloads are rejected before an
event is enqueued. The webhook returns a non-success response so Telegram may
retry transient download failures; it never silently starts an empty model
turn. Logs must contain the failure without including the bot token, file URL,
or image bytes.

### Turn and history behavior

Direct owner turns accept a `ports.Message`. Command parsing continues only for
messages without image parts; an image caption beginning with `/` is ordinary
model input so the attachment cannot be silently discarded by a command.

Steering also accepts a complete message, allowing an image sent while a turn
is running to join that turn at the next model-step boundary. The active-turn
queue already stores `ports.Message`, so this removes its current text-only
entry point rather than adding another queue.

The agent loop receives the user message rather than manufacturing one from a
string. Text-only scheduled turns and heartbeats continue using their current
entry points.

Conversation persistence deliberately stays text-only. When recording an owner
image message, Eggy stores the caption followed by `[image attached]`, or only
the marker when there was no caption. No base64 or raw bytes enter SQLite,
search results, traces, logs, or durable context. The trace input uses the same
textual representation.

### Provider translation

The OpenAI-compatible adapter preserves its current string `content` encoding
when a message has no image parts. This prevents a wire-format change for every
ordinary turn.

For a message with images, it emits the standard ordered content array:

1. A `text` part containing `Message.Content`.
2. One `image_url` part per image whose URL is a base64 data URL constructed
   from its declared media type and bytes.

Text comes first, following OpenRouter's recommended ordering. Telegram tokens,
file paths, and file IDs never reach this adapter. A provider or model that does
not support vision may return its normal request error; Eggy will surface that
failure through the existing turn-error path rather than maintain a second
capability catalog or lossy OCR model.

## Testing

Implementation follows the repository's test-first workflow:

- Telegram handler tests prove captions are used, the largest photo is chosen,
  image documents are accepted, non-image documents are rejected, download
  failures enqueue no event, and an image never becomes an empty text turn.
- Telegram client tests prove `getFile`, authenticated download URL formation,
  MIME validation, response-size limiting, cancellation, and secret-free error
  messages.
- Turn-service tests prove image messages bypass command parsing, reach the
  agent loop intact, can steer an active turn, and persist only the textual
  marker.
- OpenAI-compatible adapter tests inspect the request JSON for text-first
  multipart content and verify text-only requests remain string-valued.
- A bootstrap integration test sends a normalized image event and observes a
  multipart request at the fake model boundary.

After focused tests, run `make fmt vet test race build`. Run `make smoke` when
Docker is available; an unavailable daemon is reported as a blocker.

## Safety and resource bounds

- Existing webhook-secret verification, owner allowlisting, and update
  deduplication remain ahead of model execution.
- Downloads use the configured Telegram client and request context; no arbitrary
  URL from an update is fetched.
- A fixed 20 MB upper bound applies before base64 expansion. Only verified image
  media types proceed.
- Image bytes are not logged, traced, searched, or persisted.
- Unconfigured deployments pay nothing: Telegram remains optional, and
  text-only turns allocate no attachment data or multipart provider content.

## Deletion budget

This capability adds production code to the existing Telegram client and
handler, the existing message/event types, the existing turn path, and the
existing OpenAI-compatible translator. It adds no config keys, registered
tools, durable record types, routes, goroutines, stores, or background loops.
It deletes the text-only owner-turn plumbing where complete messages replace
string parameters. No duplicate media pipeline or generic lifecycle interface
is introduced.

If durable recall is added later, its explicit budget is a SQLite attachment
record plus retention/deletion behavior. That is a separate capability and is
not smuggled into this fix.
