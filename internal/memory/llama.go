package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hybridgroup/yzma/pkg/llama"
)

// Llama embeds with embeddinggemma-300m through llama.cpp, loaded from Go by yzma
// (purego, no cgo). Nothing is downloaded or loaded until the first Prepare.
type Llama struct {
	Assets Assets

	mu     sync.Mutex // llama.cpp contexts are not safe for concurrent use
	ready  atomic.Bool
	broken atomic.Pointer[error] // why llama.cpp can't load in this process
	model  llama.Model
	lctx   llama.Context
	vocab  llama.Vocab
	dims   int32
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
	if err := l.broken.Load(); err != nil {
		return *err
	}
	libDir, modelPath, err := l.Assets.Ensure(ctx)
	if err != nil {
		return err
	}
	// A library that doesn't load won't load later in this process either.
	broken := func(err error) error {
		err = Unavailable{Err: err, Fix: loadHint(err)}
		l.broken.Store(&err)
		return err
	}
	if err := llama.Load(libDir); err != nil {
		return broken(fmt.Errorf("load llama.cpp from %s: %w", libDir, err))
	}
	llama.LogSet(llama.LogSilent())
	if err := llama.Init(); err != nil {
		return broken(err)
	}
	// ggml loads its CPU backend from a library of its own and ignores one that fails to
	// open, which would only show as a model that fails to load.
	if llama.GGMLBackendRegCount() == 0 {
		return broken(fmt.Errorf("llama.cpp found no compute backend in %s: %w", libDir, errors.Join(llama.GGMLBackendLoadErrors(libDir)...)))
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
		_ = llama.ModelFree(m) // it fails only on a zero handle
		return err
	}
	l.model, l.lctx, l.vocab, l.dims = m, c, llama.ModelGetVocab(m), llama.ModelNEmbd(m)
	l.ready.Store(true)
	return nil
}

// Failure is why the model can't load in this process, or nil; it is an Unavailable.
func (l *Llama) Failure() error {
	if err := l.broken.Load(); err != nil {
		return *err
	}
	return nil
}

// Unavailable is a Prepare error that lasts until Studio restarts: llama.cpp's libraries
// don't load on this host. Fix, when known, says what to do.
type Unavailable struct {
	Err error
	Fix string
}

func (u Unavailable) Error() string {
	if u.Fix == "" {
		return u.Err.Error()
	}
	return u.Err.Error() + ". " + u.Fix
}

func (u Unavailable) Unwrap() error { return u.Err }

// loadHint says how to fix a library that can't find its dependencies: the prebuilt Linux
// llama.cpp is an Ubuntu build, which needs libstdc++ and libgomp where glibc looks.
func loadHint(err error) string {
	if !strings.Contains(err.Error(), "cannot open shared object file") {
		return ""
	}
	alt := " Or set STUDIO_LLAMA_LIB to the lib directory of a llama.cpp " + LlamaTag + " built for this system. Then restart Studio."
	if _, statErr := os.Stat("/etc/NIXOS"); statErr == nil {
		return "The prebuilt llama.cpp is an Ubuntu build: on NixOS, enable programs.nix-ld with programs.nix-ld.libraries = [ pkgs.stdenv.cc.cc.lib pkgs.llvmPackages.openmp ] (libstdc++ and libgomp)." + alt
	}
	return "The prebuilt llama.cpp needs libstdc++ and libgomp: install them (Debian and Ubuntu: libstdc++6 libgomp1)." + alt
}

// Close frees the model.
func (l *Llama) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.ready.Load() {
		return
	}
	l.ready.Store(false)
	// They fail only on zero handles, which a ready model doesn't have.
	_ = llama.Free(l.lctx)
	_ = llama.ModelFree(l.model)
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
	// embeddinggemma attends both ways, so llama.cpp gives its context no KV memory and
	// decodes each batch on its own; a model with memory keeps the last text's tokens.
	if mem != 0 {
		if err := llama.MemoryClear(mem, true); err != nil {
			return nil, err
		}
	}
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
