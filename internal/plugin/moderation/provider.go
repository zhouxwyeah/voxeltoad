// Package moderation provides a Pre-phase governance plugin that sends user
// message content to an external moderation API (e.g. OpenAI Moderation) and
// blocks or flags the request when violations are detected.
//
// Architecture: The ModerationProvider interface decouples the plugin from a
// specific API. The default implementation calls the OpenAI Moderation API.
// Operators can configure fail_mode ("open" or "closed") to control behavior
// when the moderation service is unavailable.
//
// Privacy: Only BlockedBy="moderation" is recorded in telemetry. Moderation
// request bodies are NEVER logged (ADR-0021 §2).
package moderation

import (
	"context"
)

// ModerationProvider checks user content against a moderation service.
// Categories is a list of moderation categories to check (empty = all).
// Returns true when content is flagged as violating policy.
type ModerationProvider interface {
	Check(ctx context.Context, content string, categories []string) (bool, error)
}
