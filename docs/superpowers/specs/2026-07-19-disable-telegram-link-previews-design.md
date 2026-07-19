# Disable Telegram Link Previews

## Problem

Eggy's Google Calendar enrollment URL is single-use. Telegram may fetch a plain URL to create a link preview, consuming the enrollment token before the owner opens it and causing `/auth/google` to return `403 forbidden`.

## Design

Ordinary text delivery through the Telegram Bot API will include `link_preview_options.is_disabled: true`. The message text and clickable URL remain unchanged. Approval messages keep their existing inline keyboard payload and OAuth enrollment remains single-use, ten-minute, and owner-initiated.

## Verification

An adapter test will capture the `sendMessage` JSON request and assert that ordinary delivery disables link previews. Existing Telegram and full repository tests must continue to pass. No provider, OAuth, state, or approval behavior changes.
