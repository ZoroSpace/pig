package ai

import (
	"time"
)

// pi: api/lazy.ts, ported into package ai because models.go consumes it (a
// separate subpackage would cycle).

func createSetupErrorMessage(model *Model, err error, startedAt int64) *AssistantMessage {
	return &AssistantMessage{
		Role:         RoleAssistant,
		Content:      []Content{},
		API:          model.API,
		Provider:     model.Provider,
		Model:        model.ID,
		Usage:        Usage{},
		StopReason:   StopReasonError,
		ErrorMessage: err.Error(),
		Timestamp:    startedAt,
	}
}

// LazyStream returns a stream synchronously while running async setup (auth
// resolution, lazy module loading) behind it. Setup failures terminate the
// stream with an error event. The setup-error message timestamps from the
// stream's wall-clock start (pi 3ba22ce17), not from when setup failed, so
// the event stream's response timing window covers it.
func LazyStream(model *Model, setup func() (*AssistantMessageEventStream, error)) *AssistantMessageEventStream {
	startedAt := time.Now().UnixMilli()
	outer := NewAssistantMessageEventStream()
	go func() {
		inner, err := setup()
		if err != nil {
			message := createSetupErrorMessage(model, err, startedAt)
			outer.Push(AssistantMessageEvent{Type: EventError, Reason: StopReasonError, Message: message})
			outer.EndWith(*message)
			return
		}
		for event := range inner.Events() {
			outer.Push(event)
		}
		outer.EndWith(inner.Result())
	}()
	return outer
}

// pi's LazyApiCapabilities and lazyApi (wrap a dynamically imported API
// module so it loads on first stream call) are missing here by design: Go has
// no dynamic import, adapters compile in. Deferred capability wrapping is
// expressed with the DeferredStreamer type assertion instead.
