// pi: test/bedrock-raw-stop-reason.test.ts.
package api

import (
	"context"
	"testing"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestBedrockRawStopReasons(t *testing.T) {
	cases := []struct {
		name          string
		stopReason    brtypes.StopReason
		wantStop      types.StopReason
		wantRaw       string
		wantErrorText string
	}{
		{
			name:       "preserves raw Bedrock stop reasons for successful stops",
			stopReason: brtypes.StopReasonEndTurn,
			wantStop:   types.StopReasonStop,
			wantRaw:    "end_turn",
		},
		{
			name:          "preserves raw Bedrock stop reasons for provider error stops",
			stopReason:    brtypes.StopReason("guardrail_intervened"),
			wantStop:      types.StopReasonError,
			wantRaw:       "guardrail_intervened",
			wantErrorText: "Provider stopped with: guardrail_intervened",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &bedrockMockClient{}
			mock.install(t)
			mock.events = []brtypes.ConverseStreamOutput{
				&brtypes.ConverseStreamOutputMemberMessageStart{Value: brtypes.MessageStartEvent{Role: brtypes.ConversationRoleAssistant}},
				&brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: tc.stopReason}},
			}

			s := streamBedrockConverse(context.Background(), bedrockBaseModel, bedrockTestContext(),
				&BedrockOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone}})
			message := s.Result()

			if message.StopReason != tc.wantStop {
				t.Fatalf("stopReason = %q, want %q", message.StopReason, tc.wantStop)
			}
			if message.RawStopReason != tc.wantRaw {
				t.Fatalf("rawStopReason = %q, want %q", message.RawStopReason, tc.wantRaw)
			}
			if tc.wantErrorText == "" {
				if message.ErrorMessage != "" {
					t.Fatalf("errorMessage = %q, want unset", message.ErrorMessage)
				}
			} else if message.ErrorMessage != tc.wantErrorText {
				t.Fatalf("errorMessage = %q, want %q", message.ErrorMessage, tc.wantErrorText)
			}
		})
	}
}
