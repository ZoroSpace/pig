package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// Port of test/custom-message.test.ts.

func TestCustomMessageComponent(t *testing.T) {
	t.Run("provides output padding to custom renderers and updates it", func(t *testing.T) {
		initTestTheme(t)
		var optionsSeen []core.MessageRenderOptions
		renderer := func(_ *harness.CustomMessage, options core.MessageRenderOptions, _ core.UITheme) tui.Component {
			optionsSeen = append(optionsSeen, options)
			return tui.NewText("custom", options.OutputPad, 0)
		}
		message := &harness.CustomMessage{
			Role:       "custom",
			CustomType: "test",
			Content:    "custom",
			Display:    true,
			Timestamp:  1,
		}
		component := NewCustomMessageComponent(message, renderer, nil, 1)

		if len(optionsSeen) != 1 || optionsSeen[0].Expanded || optionsSeen[0].OutputPad != 1 {
			t.Fatalf("optionsSeen = %+v", optionsSeen)
		}
		found := false
		for _, line := range component.Render(40) {
			if strings.HasPrefix(utils.StripAnsi(line), " custom") {
				found = true
			}
		}
		if !found {
			t.Fatal("padded custom line missing")
		}

		component.SetOutputPad(0)

		last := optionsSeen[len(optionsSeen)-1]
		if last.Expanded || last.OutputPad != 0 {
			t.Fatalf("last options = %+v", last)
		}
		found = false
		for _, line := range component.Render(40) {
			if strings.HasPrefix(utils.StripAnsi(line), "custom") {
				found = true
			}
		}
		if !found {
			t.Fatal("unpadded custom line missing")
		}
	})
}
