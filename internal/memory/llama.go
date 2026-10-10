package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/hybridgroup/yzma/pkg/llama"
)

// Llama embeds with embeddinggemma-300m through llama.cpp, loaded from Go by yzma
// (purego, no cgo). Nothing is downloaded or loaded until the first Prepare.
type Llama struct {
	Assets Assets

	mu    sync.Mutex // llama.cpp contexts are not safe for concurrent use
	ready atomic.Bool
	model llama.Model
	lctx  llama.Context
	vocab llama.Vocab
	dims  int32
}

// nCtx is embeddinggemma's context length; longer input is truncated.
const nCtx = 2048

func (l *Llama) Model() string { return ModelName }

func (l *Llama) Ready() bool { return l.ready.Load() }

// Prepare downloads the pinned assets if needed and loads the model.
func (l *Llama) Prepare(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ready.Load() {
		return nil
	}
	libDir, modelPath, err := l.Assets.Ensure(ctx)
	if err != nil {
		return err
	}
	if err := llama.Load(libDir); err != nil {
		return fmt.Errorf("load llama.cpp: %w", err)
	}
	llama.LogSet(llama.LogSilent())
	if err := llama.Init(); err != nil {
		return err
	}
	m, err := llama.ModelLoadFromFile(modelPath, llama.ModelDefaultParams())
	if err != nil {
		return fmt.Errorf("load %s: %w", modelPath, err)
	}
	if m == 0 {
		return errors.New("load embedding model: no model")
	}
	params := llama.ContextDefaultParams()
	params.NCtx, params.NBatch, params.NUbatch = nCtx, nCtx, nCtx
	params.Embeddings = 1
	params.PoolingType = llama.PoolingTypeMean
	c, err := llama.InitFromModel(m, params)
	if err != nil {
		llama.ModelFree(m)
		return err
	}
	l.model, l.lctx, l.vocab, l.dims = m, c, llama.ModelGetVocab(m), llama.ModelNEmbd(m)
	l.ready.Store(true)
	return nil
}

// Close frees the model.
func (l *Llama) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.ready.Load() {
		return
	}
	l.ready.Store(false)
	llama.Free(l.lctx)
	llama.ModelFree(l.model)
}

// EmbedDocument uses embeddinggemma's document prompt.
func (l *Llama) EmbedDocument(ctx context.Context, title, text string) ([]float32, error) {
	if title == "" {
		title = "none"
	}
	return l.embed("title: " + title + " | text: " + text)
}

// EmbedQuery uses embeddinggemma's retrieval-query prompt.
func (l *Llama) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return l.embed("task: search result | query: " + text)
}

func (l *Llama) embed(text string) ([]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.ready.Load() {
		return nil, errors.New("embedding model not loaded")
	}
	mem, err := llama.GetMemory(l.lctx)
	if err != nil {
		return nil, err
	}
	llama.MemoryClear(mem, true)
	tokens := llama.Tokenize(l.vocab, text, true, true)
	if len(tokens) > nCtx {
		tokens = tokens[:nCtx]
	}
	if _, err := llama.Decode(l.lctx, llama.BatchGetOne(tokens)); err != nil {
		return nil, err
	}
	v, err := llama.GetEmbeddingsSeq(l.lctx, 0, l.dims)
	if err != nil {
		return nil, err
	}
	return normalize(v), nil
}
