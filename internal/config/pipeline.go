package config

import (
	"fmt"
	"runtime"
	"time"
)

// Redis configures the asynq broker.
type Redis struct {
	Addr     string `yaml:"addr"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// Storage configures the S3 object store that holds original files, rendered
// page images and raw parser output. Postgres only stores the object keys.
type Storage struct {
	S3         S3Cfg         `yaml:"s3"`
	Presign    *bool         `yaml:"presign"`
	PresignTTL time.Duration `yaml:"presign_ttl"`
}

// PresignEnabled reports whether page images are served via presigned URLs.
func (s Storage) PresignEnabled() bool { return s.Presign == nil || *s.Presign }

// S3Cfg is an S3-compatible endpoint (AWS S3, MinIO, Ceph RGW).
type S3Cfg struct {
	Endpoint          string `yaml:"endpoint"`
	Region            string `yaml:"region"`
	Bucket            string `yaml:"bucket"`
	Prefix            string `yaml:"prefix"`
	AccessKey         string `yaml:"access_key"`
	SecretKey         string `yaml:"secret_key"`
	UsePathStyle      bool   `yaml:"use_path_style"`
	SSE               string `yaml:"sse"`
	PartSizeMB        int64  `yaml:"part_size_mb"`
	UploadConcurrency int    `yaml:"upload_concurrency"`
	CreateBucket      bool   `yaml:"create_bucket"`
}

// Upload limits what the document upload endpoint accepts.
type Upload struct {
	MaxBytes     int64    `yaml:"max_bytes"`
	MaxFiles     int      `yaml:"max_files"`
	AllowedTypes []string `yaml:"allowed_types"`
}

// Callback configures the optional document completion callback (§4.7).
type Callback struct {
	Timeout     time.Duration `yaml:"timeout"`      // per HTTP attempt
	MaxAttempts int           `yaml:"max_attempts"` // attempts before the delivery is marked failed
	// Backoff is the wait before attempt 2, 3, …; the last value repeats.
	Backoff []time.Duration `yaml:"backoff"`
	// SigningSecret, when set, signs each body with HMAC-SHA256
	// (X-Bepaylot-Signature: sha256=hex(hmac(secret, timestamp + "." + body))).
	SigningSecret string `yaml:"signing_secret"`
	// AllowPrivateNetworks permits callback hosts that resolve to loopback,
	// private or link-local addresses. Off in production (SSRF).
	AllowPrivateNetworks bool `yaml:"allow_private_networks"`
}

// Workers configures the asynq worker pools and per-document fairness windows.
type Workers struct {
	// Role selects which parts of the process run: api | worker | all.
	Role                  string         `yaml:"role"`
	Concurrency           map[string]int `yaml:"concurrency"`
	RenderInflightBatches int            `yaml:"render_inflight_batches"`
	RenderAheadPages      int            `yaml:"render_ahead_pages"`
	OCRInflightPages      int            `yaml:"ocr_inflight_pages"`
	HousekeepingInterval  time.Duration  `yaml:"housekeeping_interval"`
}

// RunsAPI reports whether this process serves HTTP.
func (w Workers) RunsAPI() bool { return w.Role == "api" || w.Role == "all" }

// RunsWorkers reports whether this process consumes tasks.
func (w Workers) RunsWorkers() bool { return w.Role == "worker" || w.Role == "all" }

// Parser configures page rendering, OCR and page assembly.
type Parser struct {
	DefaultEngine        string            `yaml:"default_engine"`
	PDFMode              string            `yaml:"pdf_mode"` // ocr_all | auto
	Render               Render            `yaml:"render"`
	TextLayer            TextLayer         `yaml:"text_layer"`
	ReadingOrderFix      *bool             `yaml:"reading_order_fix"`
	LowConfThreshold     float64           `yaml:"low_conf_threshold"`
	FurnitureRepeatRatio float64           `yaml:"furniture_repeat_ratio"`
	ClassMap             map[string]string `yaml:"class_map"`
	Engines              ParserEngines     `yaml:"engines"`
}

// ReadingOrderFixEnabled defaults to true.
func (p Parser) ReadingOrderFixEnabled() bool { return p.ReadingOrderFix == nil || *p.ReadingOrderFix }

// Render configures go-pdfium.
type Render struct {
	Mode              string        `yaml:"mode"` // multi_threaded | webassembly
	WorkerBin         string        `yaml:"worker_bin"`
	Workers           int           `yaml:"workers"`
	DPI               int           `yaml:"dpi"`
	MaxLongSide       int           `yaml:"max_long_side"`
	MaxPixels         int           `yaml:"max_pixels"`
	JPEGQuality       int           `yaml:"jpeg_quality"`
	BatchPages        int           `yaml:"batch_pages"`
	PageTimeout       time.Duration `yaml:"page_timeout"`
	RecycleAfterPages int           `yaml:"recycle_after_pages"`
	MaxWorkerRSSMB    int           `yaml:"max_worker_rss_mb"`
	CacheDir          string        `yaml:"cache_dir"`
	CacheMaxBytes     int64         `yaml:"cache_max_bytes"`
}

// TextLayer configures how a PDF's own text supplements OCR.
type TextLayer struct {
	Enabled            string  `yaml:"enabled"` // all | pdfa_only | off
	MinChars           int     `yaml:"min_chars"`
	MaxBadCharRatio    float64 `yaml:"max_bad_char_ratio"`
	MergeMinSimilarity float64 `yaml:"merge_min_similarity"`
}

// ParserEngines holds per-engine settings.
type ParserEngines struct {
	TurboOCR TurboOCR `yaml:"turboocr"`
	VLM      VLM      `yaml:"vlm"`
}

// VLM configures engine turboocr_vlm (§5.9): TurboOCR finds the layout, the
// regions of a page are read by a vision model through the agent
// (agent.Extract, streaming, on the LLM provider registry): regions whose
// classes share a group in one call per page, titles and tables one call
// each, seals tagged without a call. The engine is registered only when
// provider is set.
type VLM struct {
	// Provider names an entry of llm.providers (e.g. "vlm" for a local
	// olmOCR server, or the chat provider when its model reads images).
	Provider    string  `yaml:"provider"`
	Model       string  `yaml:"model"`        // "" = llm.default_model
	Prompt      string  `yaml:"prompt"`       // one region; "" = olmOCR's page prompt
	BatchPrompt string  `yaml:"batch_prompt"` // a stitched group; "" = built-in (with %d)
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
	// MaxConcurrency bounds in-flight VLM calls per worker process.
	MaxConcurrency int      `yaml:"max_concurrency"`
	Classes        []string `yaml:"classes"` // layout classes sent to the VLM; empty = text-like classes
	// Groups: group name → classes read together (one call per page and
	// group). Nil = text/caption/furniture/formula; {} = no batching.
	Groups          map[string][]string `yaml:"groups"`
	TagClasses      []string            `yaml:"tag_classes"`       // tagged without a call; nil = [seal]
	BatchMaxRegions int                 `yaml:"batch_max_regions"` // regions per batch call
	BatchMaxHeight  int                 `yaml:"batch_max_height"`  // px of a stitched batch before downscaling
	Padding         int                 `yaml:"padding"`           // px around each region
	MaxSide         int                 `yaml:"max_side"`          // downscale images above this long side
	MinSide         int                 `yaml:"min_side"`
	JPEGQuality     int                 `yaml:"jpeg_quality"`
	Retries         int                 `yaml:"retries"`   // on top of the provider's own retries
	OnError         string              `yaml:"on_error"`  // fallback (keep OCR text) | fail (retry the page)
	FullPage        *bool               `yaml:"full_page"` // no region → send the whole page
	// MinCoverage is the share of a block's OCR words that must agree with
	// the transcription before it replaces the OCR text.
	MinCoverage float64 `yaml:"min_coverage"`
	// SkipWithTextLayer skips the VLM on pages whose PDF text layer is usable.
	SkipWithTextLayer *bool `yaml:"skip_with_text_layer"`
}

// FullPageEnabled reports whether full_page is on (default true).
func (v VLM) FullPageEnabled() bool { return v.FullPage == nil || *v.FullPage }

// SkipWithTextLayerEnabled reports whether skip_with_text_layer is on (default true).
func (v VLM) SkipWithTextLayerEnabled() bool {
	return v.SkipWithTextLayer == nil || *v.SkipWithTextLayer
}

// TurboOCR is the built-in default OCR engine (POST /ocr/raw).
type TurboOCR struct {
	BaseURL string        `yaml:"base_url"`
	Timeout time.Duration `yaml:"timeout"`
	Options struct {
		Layout       *bool `yaml:"layout"`
		ReadingOrder *bool `yaml:"reading_order"`
		Tables       *bool `yaml:"tables"`
		Formulas     bool  `yaml:"formulas"`
	} `yaml:"options"`
	Breaker struct {
		Failures int           `yaml:"failures"`
		OpenFor  time.Duration `yaml:"open_for"`
	} `yaml:"breaker"`
}

// Index configures sections and the vectorless document tree.
type Index struct {
	Section struct {
		MaxTokens int `yaml:"max_tokens"`
	} `yaml:"section"`
	Tree struct {
		LLM *bool `yaml:"llm"`
		// Concurrency is the number of page calls in flight per document.
		Concurrency int `yaml:"concurrency"`
		// PageTokens caps the page text sent in one page call.
		PageTokens int `yaml:"page_tokens"`
		// MinNodeTokens: a heading with less content under it joins the
		// group before it (layout merge).
		MinNodeTokens    int    `yaml:"min_node_tokens"`
		SummaryWords     int    `yaml:"summary_words"`
		CardSummaryWords int    `yaml:"card_summary_words"`
		Provider         string `yaml:"provider"`
		Model            string `yaml:"model"`
	} `yaml:"tree"`
}

// TreeLLMEnabled defaults to true.
func (i Index) TreeLLMEnabled() bool { return i.Tree.LLM == nil || *i.Tree.LLM }

// Search configures reasoning search by walking trees: case TOC → document
// tree → pages of the chosen nodes → lines (§6.6).
type Search struct {
	Provider    string `yaml:"provider"`
	Model       string `yaml:"model"`
	DefaultMode string `yaml:"default_mode"`
	// CaseTOCBudget caps the case table of contents (cards + first
	// branches); above it branches, then summaries are cut.
	CaseTOCBudget int `yaml:"case_toc_budget"`
	// MapTokenBudget: a multi-case (kb_ids) TOC above it first picks cases.
	MapTokenBudget int `yaml:"map_token_budget"`
	// NodeReadBudget: a node above it must be expanded, not read.
	NodeReadBudget     int           `yaml:"node_read_budget"`
	MaxDocsSelected    int           `yaml:"max_docs_selected"`
	ParallelDocs       int           `yaml:"parallel_docs"`
	TreeTokenBudget    int           `yaml:"tree_token_budget"`
	FullDocTokenBudget int           `yaml:"full_doc_token_budget"`
	PageTokenBudget    int           `yaml:"page_token_budget"`
	MaxHops            int           `yaml:"max_hops"`
	MaxLLMCalls        int           `yaml:"max_llm_calls"`
	QuoteMinSimilarity float64       `yaml:"quote_min_similarity"`
	CacheTTL           time.Duration `yaml:"cache_ttl"`
	Timeout            time.Duration `yaml:"timeout"`
}

func (c *Config) applyPipelineDefaults() {
	setString(&c.Storage.S3.Region, "us-east-1")
	setString(&c.Storage.S3.Prefix, "bepaylot")
	if c.Storage.S3.PartSizeMB == 0 {
		c.Storage.S3.PartSizeMB = 16
	}
	setInt(&c.Storage.S3.UploadConcurrency, 4)
	setDuration(&c.Storage.PresignTTL, 15*time.Minute)

	if c.Upload.MaxBytes == 0 {
		c.Upload.MaxBytes = 500 << 20
	}
	setInt(&c.Upload.MaxFiles, 100)
	setDuration(&c.Callback.Timeout, 10*time.Second)
	setInt(&c.Callback.MaxAttempts, 8)
	if len(c.Callback.Backoff) == 0 {
		c.Callback.Backoff = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute,
			15 * time.Minute, 30 * time.Minute, time.Hour}
	}
	if len(c.Upload.AllowedTypes) == 0 {
		c.Upload.AllowedTypes = []string{"application/pdf", "image/jpeg", "image/png", "image/tiff"}
	}

	setString(&c.Workers.Role, "all")
	if c.Workers.Concurrency == nil {
		c.Workers.Concurrency = map[string]int{}
	}
	for pool, n := range map[string]int{"core": 4, "ocr": 8, "index": 6, "maintenance": 2} {
		if c.Workers.Concurrency[pool] <= 0 {
			c.Workers.Concurrency[pool] = n
		}
	}
	setInt(&c.Workers.RenderInflightBatches, 1)
	setInt(&c.Workers.RenderAheadPages, 32)
	setInt(&c.Workers.OCRInflightPages, 8)
	setDuration(&c.Workers.HousekeepingInterval, 5*time.Minute)

	p := &c.Parser
	setString(&p.DefaultEngine, "turboocr")
	setString(&p.PDFMode, "ocr_all")
	setString(&p.Render.Mode, "webassembly")
	setString(&p.Render.WorkerBin, "pdfium-worker")
	if p.Render.Workers <= 0 {
		p.Render.Workers = max(1, min(runtime.NumCPU()-1, 4))
	}
	setInt(&p.Render.DPI, 300)
	setInt(&p.Render.MaxLongSide, 4000)
	setInt(&p.Render.MaxPixels, 16_000_000)
	setInt(&p.Render.JPEGQuality, 85)
	setInt(&p.Render.BatchPages, 8)
	setDuration(&p.Render.PageTimeout, 30*time.Second)
	setInt(&p.Render.RecycleAfterPages, 500)
	setInt(&p.Render.MaxWorkerRSSMB, 1024)
	setString(&p.Render.CacheDir, "data/cache/pdf")
	if p.Render.CacheMaxBytes == 0 {
		p.Render.CacheMaxBytes = 20 << 30
	}
	setString(&p.TextLayer.Enabled, "all")
	setInt(&p.TextLayer.MinChars, 20)
	if p.TextLayer.MaxBadCharRatio == 0 {
		p.TextLayer.MaxBadCharRatio = 0.02
	}
	if p.TextLayer.MergeMinSimilarity == 0 {
		p.TextLayer.MergeMinSimilarity = 0.6
	}
	if p.LowConfThreshold == 0 {
		p.LowConfThreshold = 0.6
	}
	if p.FurnitureRepeatRatio == 0 {
		p.FurnitureRepeatRatio = 0.5
	}
	setDuration(&p.Engines.TurboOCR.Timeout, 120*time.Second)
	setInt(&p.Engines.TurboOCR.Breaker.Failures, 5)
	setDuration(&p.Engines.TurboOCR.Breaker.OpenFor, 30*time.Second)
	v := &p.Engines.VLM
	// A local olmOCR server configured as provider "vlm" (VLM_BASE_URL)
	// turns the engine on without naming it.
	if pc, ok := c.LLM.Providers["vlm"]; v.Provider == "" && ok && pc.BaseURL != "" {
		v.Provider = "vlm"
	}
	if v.Provider == "vlm" {
		setString(&v.Model, "allenai/olmocr-2-7b")
	}
	setInt(&v.MaxTokens, 4096)
	if v.Temperature == 0 {
		v.Temperature = 0.1
	}
	setInt(&v.MaxConcurrency, 4)
	setInt(&v.BatchMaxRegions, 20)
	setInt(&v.BatchMaxHeight, 2400)
	setInt(&v.Padding, 12)
	setInt(&v.MaxSide, 1288)
	setInt(&v.MinSide, 8)
	setInt(&v.JPEGQuality, 90)
	setString(&v.OnError, "fallback")
	if v.MinCoverage == 0 {
		v.MinCoverage = 0.3
	}

	setInt(&c.Index.Section.MaxTokens, 1500)
	setInt(&c.Index.Tree.Concurrency, 4)
	setInt(&c.Index.Tree.PageTokens, 1500)
	setInt(&c.Index.Tree.MinNodeTokens, 40)
	setInt(&c.Index.Tree.SummaryWords, 60)
	setInt(&c.Index.Tree.CardSummaryWords, 120)

	s := &c.Search
	setString(&s.DefaultMode, "reasoning")
	setInt(&s.CaseTOCBudget, 6000)
	setInt(&s.MapTokenBudget, 12000)
	setInt(&s.NodeReadBudget, 6000)
	setInt(&s.MaxDocsSelected, 5)
	setInt(&s.ParallelDocs, 4)
	setInt(&s.TreeTokenBudget, 8000)
	setInt(&s.FullDocTokenBudget, 12000)
	setInt(&s.PageTokenBudget, 24000)
	setInt(&s.MaxHops, 3)
	setInt(&s.MaxLLMCalls, 8)
	if s.QuoteMinSimilarity == 0 {
		s.QuoteMinSimilarity = 0.8
	}
	setDuration(&s.CacheTTL, 10*time.Minute)
	setDuration(&s.Timeout, 60*time.Second)
}

func (c *Config) validatePipeline() error {
	switch c.Workers.Role {
	case "api", "worker", "all":
	default:
		return fmt.Errorf("workers.role %q is not supported (want api | worker | all)", c.Workers.Role)
	}
	switch c.Parser.Render.Mode {
	case "multi_threaded", "webassembly":
	default:
		return fmt.Errorf("parser.render.mode %q is not supported (want multi_threaded | webassembly)", c.Parser.Render.Mode)
	}
	switch c.Parser.PDFMode {
	case "ocr_all", "auto":
	default:
		return fmt.Errorf("parser.pdf_mode %q is not supported (want ocr_all | auto)", c.Parser.PDFMode)
	}
	switch c.Parser.TextLayer.Enabled {
	case "all", "pdfa_only", "off":
	default:
		return fmt.Errorf("parser.text_layer.enabled %q is not supported (want all | pdfa_only | off)", c.Parser.TextLayer.Enabled)
	}
	switch c.Search.DefaultMode {
	case "reasoning", "keyword", "metadata":
	default:
		return fmt.Errorf("search.default_mode %q is not supported", c.Search.DefaultMode)
	}
	return nil
}
