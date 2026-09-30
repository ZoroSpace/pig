// pi: test/model-types.test.ts (v0.99.1 #9948) — model type discrimination:
// legacy chat entries without a type, mixed-list narrowing, and unknown-type
// drops on restore/fetch. The built-in getter compile-time regression check
// (literal model ids) has no Go counterpart.
package ai

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/samber/lo"
)

func chatModelWithoutType(provider, id string) Model {
	return Model{
		ID: id, Name: id, API: ApiOpenAICompletions, Provider: ProviderID(provider),
		BaseURL: "https://example.test/v1", Reasoning: false,
		Input: []ModelInput{ModelInputText},
		Cost:  ModelCost{},
		Type:  "", // legacy chat: no type
	}
}

func imageModelTyped(provider, id string) Model {
	return Model{
		ID: id, Name: id, API: ImageApiOpenRouter, Provider: ProviderID(provider),
		BaseURL: "https://example.test/v1",
		Input:   []ModelInput{ModelInputText}, Output: []ModelInput{ModelInputImage},
		Cost: ModelCost{}, Type: ModelTypeImage,
	}
}

func TestModelTypesLegacyChatWithoutType(t *testing.T) {
	chat := chatModelWithoutType("handwritten", "m")
	provider := &stubProvider{
		id:      "handwritten",
		streams: stubStreams{},
		models:  []Model{chat},
		auth:    auth.ProviderAuth{APIKey: &stubAuth{key: "k"}},
	}
	models := NewModels(nil)
	models.SetProvider(provider)

	got, ok := models.GetModel("handwritten", "m")
	if !ok {
		t.Fatal("GetModel = not found")
	}
	if got.Type != "" {
		t.Fatalf("Type = %q, want empty", got.Type)
	}
	if got.GetModelType() != ModelTypeChat {
		t.Fatalf("GetModelType() = %q, want chat", got.GetModelType())
	}
	typed := got
	typed.Type = ModelTypeChat
	if !ModelsAreEqual(&got, &typed) {
		t.Fatal("ModelsAreEqual(model, model{type:chat}) = false, want true")
	}
	if ModelsAreEqual(&got, &Model{ID: got.ID, Provider: got.Provider, Type: ModelTypeImage}) {
		t.Fatal("ModelsAreEqual(model, image entry) = true, want false")
	}
	chatList := models.GetModelsOfType(ModelTypeChat)
	if len(chatList) != 1 || chatList[0].ID != "m" {
		t.Fatalf("GetModelsOfType(chat) = %+v", chatList)
	}
	all := models.GetAllModels()
	if len(all) != 1 || all[0].ID != "m" {
		t.Fatalf("GetAllModels() = %+v", all)
	}
	if image := models.GetModelsOfType(ModelTypeImage); len(image) != 0 {
		t.Fatalf("GetModelsOfType(image) = %+v, want empty", image)
	}
}

func TestModelTypesMixedListNarrowing(t *testing.T) {
	untyped := chatModelWithoutType("p", "c")
	typed := chatModelWithoutType("p", "typed")
	typed.Type = ModelTypeChat
	image := imageModelTyped("p", "i")
	mixed := []Model{untyped, typed, image}

	var chats, images []string
	classifiers := 0
	for _, model := range mixed {
		switch {
		case model.IsModelType(ModelTypeChat):
			chats = append(chats, model.ID)
		case model.IsModelType(ModelTypeImage):
			images = append(images, model.ID)
		case model.IsModelType(ModelTypeClassifier):
			classifiers++
		}
	}
	if len(chats) != 2 || chats[0] != "c" || chats[1] != "typed" {
		t.Fatalf("chat narrowing = %v", chats)
	}
	if len(images) != 1 || images[0] != "i" {
		t.Fatalf("image narrowing = %v", images)
	}
	if classifiers != 0 {
		t.Fatalf("classifier narrowing = %d entries, want 0", classifiers)
	}
}

func TestModelTypesUnknownTypesDropped(t *testing.T) {
	noNetwork := lo.ToPtr(false)
	store := &InMemoryModelsStore{}
	stored := &ModelsStoreEntry{Models: []Model{
		chatModelWithoutType("dyn", "stored-chat"),
		imageModelTyped("dyn", "stored-image"),
		{ID: "future-embedding", Name: "future-embedding", API: ApiOpenAICompletions, Provider: "dyn",
			Input: []ModelInput{ModelInputText}, Type: ModelType("embedding")},
		{ID: "future-video", Name: "future-video", API: ImageApiOpenRouter, Provider: "dyn",
			Input: []ModelInput{ModelInputText}, Output: []ModelInput{ModelInputImage}, Type: ModelType("video")},
	}}
	if err := store.Write(context.Background(), "dyn", stored); err != nil {
		t.Fatal(err)
	}

	var fetched []Model
	models := NewModels(&CreateModelsOptions{ModelsStore: store})
	models.SetProvider(CreateProvider(&CreateProviderOptions{
		ID:   "dyn",
		Auth: auth.ProviderAuth{APIKey: &stubAuth{key: "k"}},
		FetchModels: func(ctx context.Context, refreshCtx *RefreshModelsContext) ([]Model, error) {
			return fetched, nil
		},
	}))

	restored := models.Refresh(context.Background(), &ModelsRefreshOptions{Providers: []string{"dyn"}, AllowNetwork: noNetwork})
	if len(restored.Errors) != 0 {
		t.Fatalf("restore errors: %v", restored.Errors)
	}
	assertModelIDs(t, models.GetAllModels(), []string{"stored-chat", "stored-image"})

	fetched = []Model{
		chatModelWithoutType("dyn", "fetched-chat"),
		{ID: "fetched-video", Name: "fetched-video", API: ImageApiOpenRouter, Provider: "dyn",
			Input: []ModelInput{ModelInputText}, Output: []ModelInput{ModelInputImage}, Type: ModelType("video")},
	}
	refreshed := models.Refresh(context.Background(), &ModelsRefreshOptions{Providers: []string{"dyn"}})
	if len(refreshed.Errors) != 0 {
		t.Fatalf("refresh errors: %v", refreshed.Errors)
	}
	assertModelIDs(t, models.GetAllModels(), []string{"fetched-chat"})
	entry, err := store.Read(context.Background(), "dyn")
	if err != nil || entry == nil {
		t.Fatalf("store read: %v", err)
	}
	assertModelIDs(t, entry.Models, []string{"fetched-chat"})
}

func assertModelIDs(t *testing.T, models []Model, want []string) {
	t.Helper()
	if len(models) != len(want) {
		t.Fatalf("model ids = %+v, want %v", ids(models), want)
	}
	for i, model := range models {
		if model.ID != want[i] {
			t.Fatalf("model ids = %+v, want %v", ids(models), want)
		}
	}
}

func ids(models []Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}
