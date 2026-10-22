package ai

import (
	"context"

	"github.com/ZoroSpace/pig/ai/utils"
)

// withClientIdentity returns models with identity as the default identity of
// its requests (pi: withClientIdentity in utils/identity.ts). It lives in the
// ai package rather than ai/utils because the Models interface lives here and
// Go rejects the import cycle. A MutableModels input yields a MutableModels
// result, which callers recover by type assertion.
func WithClientIdentity(models Models, identity *ClientIdentity) Models {
	wrapped := &clientIdentityModels{Models: models, identity: identity}
	if mutable, ok := models.(MutableModels); ok {
		return &clientIdentityMutableModels{clientIdentityModels: wrapped, mutable: mutable}
	}
	return wrapped
}

type clientIdentityModels struct {
	Models
	identity *ClientIdentity
}

func (m *clientIdentityModels) merge(identity *ClientIdentity) *ClientIdentity {
	return utils.MergeClientIdentity(m.identity, identity)
}

func (m *clientIdentityModels) Stream(ctx context.Context, model *Model, llmContext *Context, options *ModelsStreamOptions) *AssistantMessageEventStream {
	next := &ModelsStreamOptions{}
	if options != nil {
		copied := *options
		next = &copied
	}
	next.Identity = m.merge(next.Identity)
	return m.Models.Stream(ctx, model, llmContext, next)
}

func (m *clientIdentityModels) Complete(ctx context.Context, model *Model, llmContext *Context, options *ModelsStreamOptions) (*AssistantMessage, error) {
	next := &ModelsStreamOptions{}
	if options != nil {
		copied := *options
		next = &copied
	}
	next.Identity = m.merge(next.Identity)
	return m.Models.Complete(ctx, model, llmContext, next)
}

func (m *clientIdentityModels) StreamSimple(ctx context.Context, model *Model, llmContext *Context, options *ModelsSimpleStreamOptions) *AssistantMessageEventStream {
	next := &ModelsSimpleStreamOptions{}
	if options != nil {
		copied := *options
		next = &copied
	}
	next.Identity = m.merge(next.Identity)
	return m.Models.StreamSimple(ctx, model, llmContext, next)
}

func (m *clientIdentityModels) CompleteSimple(ctx context.Context, model *Model, llmContext *Context, options *ModelsSimpleStreamOptions) (*AssistantMessage, error) {
	next := &ModelsSimpleStreamOptions{}
	if options != nil {
		copied := *options
		next = &copied
	}
	next.Identity = m.merge(next.Identity)
	return m.Models.CompleteSimple(ctx, model, llmContext, next)
}

func (m *clientIdentityModels) StreamDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) *AssistantMessageEventStream {
	next := &ModelsDeferredFetchOptions{}
	if options != nil {
		copied := *options
		next = &copied
	}
	next.Identity = m.merge(next.Identity)
	return m.Models.StreamDeferred(ctx, model, handle, next)
}

func (m *clientIdentityModels) FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) (*AssistantMessage, error) {
	next := &ModelsDeferredFetchOptions{}
	if options != nil {
		copied := *options
		next = &copied
	}
	next.Identity = m.merge(next.Identity)
	return m.Models.FetchDeferred(ctx, model, handle, next)
}

func (m *clientIdentityModels) CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredCancelOptions) error {
	next := &ModelsDeferredCancelOptions{}
	if options != nil {
		copied := *options
		next = &copied
	}
	next.Identity = m.merge(next.Identity)
	return m.Models.CancelDeferred(ctx, model, handle, next)
}

type clientIdentityMutableModels struct {
	*clientIdentityModels
	mutable MutableModels
}

func (m *clientIdentityMutableModels) SetProvider(provider Provider) { m.mutable.SetProvider(provider) }
func (m *clientIdentityMutableModels) DeleteProvider(id string)      { m.mutable.DeleteProvider(id) }
func (m *clientIdentityMutableModels) ClearProviders()               { m.mutable.ClearProviders() }
