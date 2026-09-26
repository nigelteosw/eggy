// Package events defines what enters the daemon from outside: an owner message,
// an approval decision, a scheduled run. Every surface (Telegram, Discord, the
// web chat, the scheduler) turns its input into an Event, and the dispatcher
// handles each one exactly once.
package events
