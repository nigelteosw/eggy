// Package openaicompat is the model adapter for OpenAI's Chat Completions wire
// format. OpenAI, DeepSeek, OpenRouter, and Groq all speak it, so adding one of
// them is a providers entry in config.yaml, not new code. It also discovers a
// provider's model catalog and applies OpenRouter's caching and routing options.
package openaicompat
