// Package telegram is the Telegram channel adapter: the Bot API client and the
// webhook that turns updates into events.
//
// handler.go authenticates the webhook, allowlists senders, deduplicates
// updates, and handles pairing; client.go sends messages, approval buttons,
// and edits; format.go renders Markdown as Telegram HTML and splits long
// messages; media.go downloads images and documents; and select.go is the
// telegram_select tool for agent-written inline keyboards, which can never
// satisfy an approval.
package telegram
