package benchmark

import (
	"runtime"
	"runtime/debug"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

type Report struct {
	SchemaVersion int            `json:"schema_version"`
	Input         string         `json:"input"`
	Settings      Settings       `json:"settings"`
	Runtime       Runtime        `json:"runtime"`
	Source        Source         `json:"source"`
	Cases         []Case         `json:"cases"`
	Engines       []EngineReport `json:"engines"`
	Notes         []string       `json:"notes"`
}

type Settings struct {
	MaxEvents     int     `json:"max_events"`
	Samples       int     `json:"samples"`
	WindowMS      float64 `json:"window_ms"`
	MinIterations int     `json:"min_iterations"`
	MaxIterations int     `json:"max_iterations"`
	Timeout       string  `json:"timeout"`
	WarmupQueries int     `json:"warmup_queries_per_case"`
}

type Runtime struct {
	GoVersion        string            `json:"go_version"`
	OS               string            `json:"os"`
	Arch             string            `json:"arch"`
	GOMAXPROCS       int               `json:"gomaxprocs"`
	MemoryLimitBytes int64             `json:"memory_limit_bytes"`
	MainVersion      string            `json:"main_version"`
	BuildSettings    map[string]string `json:"build_settings"`
}

type Source struct {
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	Events     int    `json:"events"`
	Services   int    `json:"services"`
	MinUS      int64  `json:"min_us"`
	MaxUS      int64  `json:"max_us"`
	TimeSpanUS uint64 `json:"time_span_us"`
}

type Case struct {
	Name            string       `json:"name"`
	SpanDivisor     uint64       `json:"span_divisor"`
	ActualWidthUS   uint64       `json:"actual_width_us"`
	Filter          query.Filter `json:"filter"`
	TimeMatchedRows uint64       `json:"time_matched_rows"`
}

type EngineReport struct {
	Engine query.Engine `json:"engine"`
	Load   Load         `json:"load"`
	Heap   Heap         `json:"heap"`
	Cases  []CaseReport `json:"cases"`
}

type Load struct {
	MS     *float64 `json:"ms"`
	SHA256 string   `json:"sha256"`
	Bytes  int64    `json:"bytes"`
}

type Heap struct {
	BeforeBytes uint64 `json:"before_heap_alloc_bytes"`
	AfterBytes  uint64 `json:"after_heap_alloc_bytes"`
	DeltaBytes  int64  `json:"delta_heap_alloc_bytes"`
}

type CaseReport struct {
	Name      string       `json:"name"`
	Result    query.Result `json:"result"`
	Samples   []Sample     `json:"samples"`
	BatchMean Summary      `json:"batch_mean_ms"`
}

type Sample struct {
	Iterations   int      `json:"iterations"`
	ElapsedMS    *float64 `json:"elapsed_ms"`
	BatchMeanMS  *float64 `json:"batch_mean_ms"`
	StableWindow bool     `json:"stable_window"`
}

type Summary struct {
	Min          *float64 `json:"min"`
	Median       *float64 `json:"median"`
	Max          *float64 `json:"max"`
	StableWindow bool     `json:"stable_window"`
}

func runtimeInfo() Runtime {
	result := Runtime{
		GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		GOMAXPROCS: runtime.GOMAXPROCS(0), MemoryLimitBytes: debug.SetMemoryLimit(-1),
		BuildSettings: map[string]string{},
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		result.MainVersion = info.Main.Version
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs", "vcs.revision", "vcs.time", "vcs.modified", "-buildmode", "-compiler", "CGO_ENABLED":
				result.BuildSettings[setting.Key] = setting.Value
			}
		}
	}
	return result
}
