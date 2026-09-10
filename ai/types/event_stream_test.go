package types

import (
	"reflect"
	"testing"
)

func TestEventStreamPushIterateResult(t *testing.T) {
	s := NewEventStream(
		func(ev int) bool { return ev == 3 },
		func(ev int) int { return ev * 10 },
	)
	go func() {
		s.Push(1)
		s.Push(2)
		s.Push(3)
		s.Push(4) // ignored: stream is done
	}()
	var got []int
	for ev := range s.Events() {
		got = append(got, ev)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("events: %v", got)
	}
	if r := s.Result(); r != 30 {
		t.Fatalf("result: %d", r)
	}
}

func TestEventStreamEndFlushesWaiter(t *testing.T) {
	s := NewEventStream(func(ev int) bool { return false }, func(ev int) int { return ev })
	got := make(chan []int, 1)
	go func() {
		var out []int
		for ev := range s.Events() {
			out = append(out, ev)
		}
		got <- out
	}()
	s.Push(7)
	s.End()
	if out := <-got; len(out) != 1 || out[0] != 7 {
		t.Fatalf("events: %v", out)
	}
	var zero int
	if r := s.Result(); r != zero {
		t.Fatalf("result: %d", r)
	}
}

func TestEventStreamEndWith(t *testing.T) {
	s := NewEventStream(func(ev string) bool { return false }, func(ev string) string { return ev })
	s.EndWith("final")
	var got []string
	for ev := range s.Events() {
		got = append(got, ev)
	}
	if len(got) != 0 {
		t.Fatalf("events: %v", got)
	}
	if r := s.Result(); r != "final" {
		t.Fatalf("result: %q", r)
	}
}

func TestEventStreamFirstResolutionWins(t *testing.T) {
	s := NewEventStream(func(ev int) bool { return ev == 1 }, func(ev int) int { return ev })
	s.Push(1) // resolves result to 1
	s.EndWith(99)
	if r := s.Result(); r != 1 {
		t.Fatalf("result: %d", r)
	}
}

func TestAssistantMessageEventStream(t *testing.T) {
	s := NewAssistantMessageEventStream()
	msg := &AssistantMessage{
		Role:       RoleAssistant,
		Content:    []Content{NewTextContent("hi")},
		API:        ApiOpenAICompletions,
		Provider:   "openai",
		Model:      "gpt-x",
		StopReason: StopReasonStop,
	}
	go func() {
		s.Push(AssistantMessageEvent{Type: EventTextDelta, ContentIndex: 0, Delta: "h", Partial: msg})
		s.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: msg})
	}()
	var deltas int
	for ev := range s.Events() {
		if ev.Type == EventTextDelta {
			deltas++
		}
	}
	if deltas != 1 {
		t.Fatalf("deltas: %d", deltas)
	}
	r := s.Result()
	if r.StopReason != StopReasonStop || r.Text() != "hi" {
		t.Fatalf("result: %+v", r)
	}
}

func TestAssistantMessageEventStreamError(t *testing.T) {
	s := NewAssistantMessageEventStream()
	errMsg := &AssistantMessage{
		Role:         RoleAssistant,
		Content:      []Content{},
		API:          ApiOpenAICompletions,
		Provider:     "openai",
		Model:        "gpt-x",
		StopReason:   StopReasonError,
		ErrorMessage: "boom",
	}
	s.Push(AssistantMessageEvent{Type: EventError, Reason: StopReasonError, Message: errMsg})
	r := s.Result()
	if r.StopReason != StopReasonError || r.ErrorMessage != "boom" {
		t.Fatalf("result: %+v", r)
	}
}
