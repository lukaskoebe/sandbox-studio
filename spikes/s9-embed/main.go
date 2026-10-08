// Spike S9: local embeddings with llama.cpp via yzma (purego) and embeddinggemma-300m.
// Measures load time, single-query latency, batch throughput and a retrieval sanity check.
//
// Run: go run ./spikes/s9-embed -dir /some/cache/dir
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/hybridgroup/yzma/pkg/download"
	"github.com/hybridgroup/yzma/pkg/llama"
)

const modelURL = "https://huggingface.co/ggml-org/embeddinggemma-300M-GGUF/resolve/main/embeddinggemma-300M-Q8_0.gguf"

func main() {
	dir := flag.String("dir", "", "cache directory for llama.cpp libs and the model")
	flag.Parse()
	if *dir == "" {
		log.Fatal("-dir is required")
	}
	lib := filepath.Join(*dir, "llama")
	model := filepath.Join(*dir, "embeddinggemma-300M-Q8_0.gguf")

	t := time.Now()
	if !download.AlreadyInstalled(lib) {
		if err := download.Get(runtime.GOARCH, runtime.GOOS, download.CPU.String(), "", lib); err != nil {
			log.Fatalf("llama.cpp download: %v", err)
		}
	}
	if _, err := os.Stat(model); err != nil {
		if err := fetch(modelURL, model); err != nil {
			log.Fatalf("model download: %v", err)
		}
	}
	log.Printf("assets ready in %s", time.Since(t).Round(time.Millisecond))

	if err := llama.Load(lib); err != nil {
		log.Fatal(err)
	}
	llama.LogSet(llama.LogSilent())
	llama.Init()
	defer llama.Close()

	t = time.Now()
	m, err := llama.ModelLoadFromFile(model, llama.ModelDefaultParams())
	if err != nil || m == 0 {
		log.Fatalf("load model: %v", err)
	}
	defer llama.ModelFree(m)
	params := llama.ContextDefaultParams()
	params.NCtx = 2048
	params.NBatch = 2048
	params.NUbatch = 2048
	params.Embeddings = 1
	params.PoolingType = llama.PoolingTypeMean
	ctx, err := llama.InitFromModel(m, params)
	if err != nil {
		log.Fatal(err)
	}
	defer llama.Free(ctx)
	log.Printf("model loaded in %s, dims=%d, rss=%s", time.Since(t).Round(time.Millisecond), llama.ModelNEmbd(m), rss())

	vocab := llama.ModelGetVocab(m)
	embed := func(text string) []float32 {
		mem, err := llama.GetMemory(ctx)
		if err != nil {
			log.Fatal(err)
		}
		llama.MemoryClear(mem, true)
		tokens := llama.Tokenize(vocab, text, true, true)
		if _, err := llama.Decode(ctx, llama.BatchGetOne(tokens)); err != nil {
			log.Fatal(err)
		}
		v, err := llama.GetEmbeddingsSeq(ctx, 0, llama.ModelNEmbd(m))
		if err != nil {
			log.Fatal(err)
		}
		return normalize(v)
	}

	docs := []string{
		"The user prefers pnpm over npm for all JavaScript projects.",
		"Deploy the API with `make deploy ENV=staging` after running the integration tests.",
		"Postgres runs on port 5433 in the dev environment because 5432 is taken by the host.",
		"Lukas likes short, single-line commit messages.",
		"The frontend uses TanStack Router without TanStack Start.",
		"Never push directly to main; open a pull request on Forgejo instead.",
		"The CI pipeline caches Go modules keyed by go.sum.",
		"Rust toolchain is pinned to 1.85 via rust-toolchain.toml.",
	}
	queries := map[string]int{
		"which package manager should I use for node?":      0,
		"how do I ship the backend to staging?":             1,
		"database connection refused on 5432":               2,
		"how should I write my commit message":              3,
		"can I push my changes straight to the main branch": 5,
	}

	t = time.Now()
	vecs := make([][]float32, len(docs))
	for i, d := range docs {
		vecs[i] = embed("title: none | text: " + d)
	}
	perDoc := time.Since(t) / time.Duration(len(docs))
	log.Printf("embedded %d docs, %s/doc", len(docs), perDoc.Round(time.Microsecond))

	hits, n := 0, 0
	var total time.Duration
	keys := make([]string, 0, len(queries))
	for q := range queries {
		keys = append(keys, q)
	}
	sort.Strings(keys)
	for _, q := range keys {
		t = time.Now()
		qv := embed("task: search result | query: " + q)
		total += time.Since(t)
		n++
		best, bestScore := -1, -2.0
		for i, v := range vecs {
			if s := dot(qv, v); s > bestScore {
				best, bestScore = i, s
			}
		}
		ok := best == queries[q]
		if ok {
			hits++
		}
		fmt.Printf("%-52q -> %.3f %q ok=%v\n", q, bestScore, trunc(docs[best]), ok)
	}
	fmt.Printf("query latency avg %s, top-1 %d/%d, rss %s\n", (total / time.Duration(n)).Round(time.Microsecond), hits, n, rss())
}

func fetch(url, dst string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func normalize(v []float32) []float32 {
	var s float64
	for _, x := range v {
		s += float64(x * x)
	}
	n := float32(1 / math.Sqrt(s))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * n
	}
	return out
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i] * b[i])
	}
	return s
}

func trunc(s string) string {
	if len(s) > 48 {
		return s[:48] + "…"
	}
	return s
}

func rss() string {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "n/a"
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "VmRSS:"))
		}
	}
	return "n/a"
}
