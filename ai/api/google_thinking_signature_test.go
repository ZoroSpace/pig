// pi: test/google-thinking-signature.test.ts.
package api

import "testing"

func TestGoogleThinkingDetectionThoughtTrue(t *testing.T) {
	if !IsThinkingPart(&googlePart{Thought: true}) {
		t.Error("thought=true with no signature should be thinking")
	}
	if !IsThinkingPart(&googlePart{Thought: true, ThoughtSignature: "opaque-signature"}) {
		t.Error("thought=true with a signature should be thinking")
	}
}

func TestGoogleThinkingDetectionSignatureAloneIsNotThinking(t *testing.T) {
	// Per Google docs, thoughtSignature is for context replay and can appear
	// on any part type. Only thought === true indicates thinking content.
	// See: https://ai.google.dev/gemini-api/docs/thought-signatures
	if IsThinkingPart(&googlePart{ThoughtSignature: "opaque-signature"}) {
		t.Error("thoughtSignature alone should not be thinking")
	}
	if IsThinkingPart(&googlePart{ThoughtSignature: "opaque-signature"}) {
		t.Error("thought=false with a signature should not be thinking")
	}
}

func TestGoogleThinkingDetectionEmptySignature(t *testing.T) {
	if IsThinkingPart(&googlePart{}) {
		t.Error("no thought and no signature should not be thinking")
	}
	if IsThinkingPart(&googlePart{ThoughtSignature: ""}) {
		t.Error("thought=false and empty signature should not be thinking")
	}
}

func TestGoogleRetainThoughtSignaturePreservesExisting(t *testing.T) {
	first := RetainThoughtSignature("", "sig-1")
	if first != "sig-1" {
		t.Errorf("first = %q, want sig-1", first)
	}

	second := RetainThoughtSignature(first, "")
	if second != "sig-1" {
		t.Errorf("second = %q, want sig-1", second)
	}

	third := RetainThoughtSignature(second, "")
	if third != "sig-1" {
		t.Errorf("third = %q, want sig-1", third)
	}
}

func TestGoogleRetainThoughtSignatureUpdatesOnNewSignature(t *testing.T) {
	updated := RetainThoughtSignature("sig-1", "sig-2")
	if updated != "sig-2" {
		t.Errorf("updated = %q, want sig-2", updated)
	}
}
