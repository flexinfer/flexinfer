package backend

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestParseVLLMVersion(t *testing.T) {
	tests := []struct {
		raw     string
		want    vllmRelease
		wantErr bool
	}{
		{raw: "0.33.0", want: vllmRelease{0, 33, 0}},
		{raw: "v0.32.0", want: vllmRelease{0, 32, 0}},
		{raw: " 0.33.1+rocm72 ", want: vllmRelease{0, 33, 1}},
		{raw: "0.33", want: vllmRelease{0, 33, 0}},
		{raw: "1.0.0", want: vllmRelease{1, 0, 0}},
		{raw: "", wantErr: true},
		{raw: "0", wantErr: true},
		{raw: "latest", wantErr: true},
		{raw: "0.33.0rc1", wantErr: true},
		{raw: "0.33.0.dev12", wantErr: true},
		{raw: "0.-1.0", wantErr: true},
		{raw: "0.+33.0", wantErr: true},
		{raw: "0..0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := parseVLLMVersion(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseVLLMVersion(%q) = %v, want an error", tt.raw, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parseVLLMVersion(%q) = %v, %v; want %v", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestVLLMReleaseAtLeast(t *testing.T) {
	v033 := vllmRelease{0, 33, 0}
	for _, tt := range []struct {
		v    vllmRelease
		want bool
	}{
		{vllmRelease{0, 32, 9}, false},
		{vllmRelease{0, 33, 0}, true},
		{vllmRelease{0, 33, 1}, true},
		{vllmRelease{0, 34, 0}, true},
		{vllmRelease{1, 0, 0}, true},
	} {
		if got := tt.v.atLeast(v033); got != tt.want {
			t.Errorf("%v.atLeast(%v) = %v, want %v", tt.v, v033, got, tt.want)
		}
	}
}

func TestValidateVLLMConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantErr string
	}{
		{name: "empty config", cfg: nil},
		{name: "declared release", cfg: map[string]any{"vllmVersion": "0.33.0"}},
		{name: "partial prefill cap", cfg: map[string]any{"maxNumPartialPrefills": 2.0}, wantErr: "spec.config.maxNumPartialPrefills is not supported: vLLM has no --max-num-partial-prefills flag"},
		{name: "long partial prefill cap", cfg: map[string]any{"maxLongPartialPrefills": 1.0}, wantErr: "spec.config.maxLongPartialPrefills is not supported: vLLM has no --max-long-partial-prefills flag"},
		{name: "partial prefill cap set to zero", cfg: map[string]any{"maxNumPartialPrefills": 0.0}, wantErr: "maxNumPartialPrefills is not supported"},
		{name: "unquoted release", cfg: map[string]any{"vllmVersion": 0.33}, wantErr: "spec.config.vllmVersion must be a quoted string"},
		{name: "unreadable release", cfg: map[string]any{"vllmVersion": "latest"}, wantErr: `spec.config.vllmVersion "latest" is not a vLLM release`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVLLMConfig(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateVLLMConfig() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateVLLMConfig() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

// A Model that slipped past validation still never renders the dead flags.
func TestVLLMBackendArgs_DeadPartialPrefillKeysNotRendered(t *testing.T) {
	args := (&VLLMBackend{}).Args(&ModelSpec{Model: "m", Config: map[string]any{
		"maxNumPartialPrefills":  2,
		"maxLongPartialPrefills": 1,
	}})
	for _, flag := range []string{"--max-num-partial-prefills", "--max-long-partial-prefills"} {
		if containsVLLMArg(args, flag) {
			t.Errorf("%s rendered in %v", flag, args)
		}
	}
}

func TestVLLMBackendArgs_MambaCacheModeFollowsDeclaredVersion(t *testing.T) {
	tests := []struct {
		version string // "" leaves vllmVersion unset
		want    bool
	}{
		{version: "", want: true},
		{version: "0.32.0", want: true},
		{version: "0.33.0", want: false},
		{version: "v0.34.1+rocm72", want: false},
		{version: "latest", want: true}, // unreadable: ValidateVLLMConfig refuses it, the render stays legacy
	}
	for _, tt := range tests {
		t.Run("version="+tt.version, func(t *testing.T) {
			cfg := map[string]any{"mambaCacheMode": "align", "enablePrefixCaching": true}
			if tt.version != "" {
				cfg["vllmVersion"] = tt.version
			}
			args := (&VLLMBackend{}).Args(&ModelSpec{Model: "m", Config: cfg})
			value, rendered := flagValue(args, "--mamba-cache-mode")
			if rendered != tt.want || (rendered && value != "align") {
				t.Fatalf("--mamba-cache-mode rendered=%v value=%q, want rendered=%v in %v", rendered, value, tt.want, args)
			}
			if !containsVLLMArg(args, "--enable-prefix-caching") {
				t.Fatalf("--enable-prefix-caching missing from %v", args)
			}
			if containsVLLMArg(args, "--vllm-version") || containsVLLMArg(args, tt.version) && tt.version != "" {
				t.Fatalf("vllmVersion reached the command line: %v", args)
			}
		})
	}
}

func TestVLLMBackendArgs_PrefixCacheSleepAndConnectorKeys(t *testing.T) {
	b := &VLLMBackend{}

	unset := b.Args(&ModelSpec{Model: "m"})
	for _, flag := range []string{
		"--prefix-match-unit",
		"--enable-mamba-shared-prefix-checkpoint",
		"--no-enable-mamba-shared-prefix-checkpoint",
		"--kv-transfer-config",
		"--sleep-preserve-parameter-names",
	} {
		if containsVLLMArg(unset, flag) {
			t.Errorf("%s rendered without its key: %v", flag, unset)
		}
	}

	// Values as the controller decodes them from the Model's JSON config.
	args := b.Args(&ModelSpec{Model: "m", Config: map[string]any{
		"prefixMatchUnit":                   64.0,
		"enableMambaSharedPrefixCheckpoint": true,
		"kvTransferConfig": map[string]any{
			"kv_connector": "OffloadingConnector",
			"kv_role":      "kv_both",
		},
		"enableSleepMode":             true,
		"sleepPreserveParameterNames": []any{"draft.*", "*.engram.embed_tokens.weight*"},
	}})
	if v, _ := flagValue(args, "--prefix-match-unit"); v != "64" {
		t.Errorf("--prefix-match-unit = %q, want 64 in %v", v, args)
	}
	if !containsVLLMArg(args, "--enable-mamba-shared-prefix-checkpoint") {
		t.Errorf("--enable-mamba-shared-prefix-checkpoint missing from %v", args)
	}
	if v, _ := flagValue(args, "--kv-transfer-config"); v != `{"kv_connector":"OffloadingConnector","kv_role":"kv_both"}` {
		t.Errorf("--kv-transfer-config = %q in %v", v, args)
	}
	i := slices.Index(args, "--sleep-preserve-parameter-names")
	if i < 0 || i+2 >= len(args) || !reflect.DeepEqual(args[i+1:i+3], []string{"draft.*", "*.engram.embed_tokens.weight*"}) {
		t.Errorf("--sleep-preserve-parameter-names must take one token per glob, got %v", args)
	}

	// An encoded string passes through as-is, and false renders the --no- form.
	args = b.Args(&ModelSpec{Model: "m", Config: map[string]any{
		"enableMambaSharedPrefixCheckpoint": false,
		"kvTransferConfig":                  `{"kv_connector":"OffloadingConnector"}`,
		"sleepPreserveParameterNames":       "draft.*",
		"prefixMatchUnit":                   0,
	}})
	if !containsVLLMArg(args, "--no-enable-mamba-shared-prefix-checkpoint") {
		t.Errorf("--no-enable-mamba-shared-prefix-checkpoint missing from %v", args)
	}
	if v, _ := flagValue(args, "--kv-transfer-config"); v != `{"kv_connector":"OffloadingConnector"}` {
		t.Errorf("--kv-transfer-config = %q in %v", v, args)
	}
	if v, _ := flagValue(args, "--sleep-preserve-parameter-names"); v != "draft.*" {
		t.Errorf("--sleep-preserve-parameter-names = %q in %v", v, args)
	}
	if containsVLLMArg(args, "--prefix-match-unit") {
		t.Errorf("--prefix-match-unit rendered for 0 (vLLM requires > 0): %v", args)
	}
}

func TestVLLMBackendEnv_DeclaredVersionDropsUndefinedVariables(t *testing.T) {
	disabled := []string{
		"VLLM_ROCM_USE_AITER",
		"VLLM_ROCM_USE_AITER_LINEAR",
		"VLLM_ROCM_USE_AITER_MOE",
		"VLLM_ROCM_USE_AITER_RMSNORM",
		"VLLM_ROCM_USE_AITER_MLA",
		"VLLM_ROCM_USE_AITER_MHA",
		"VLLM_ROCM_USE_AITER_TRITON_ROPE",
		"VLLM_ROCM_USE_AITER_FP8BMM",
		"VLLM_ROCM_USE_AITER_FP4BMM",
		"VLLM_ROCM_USE_AITER_UNIFIED_ATTENTION",
		"VLLM_ROCM_USE_AITER_FUSION_SHARED_EXPERTS",
		"VLLM_ROCM_USE_AITER_TRITON_GEMM",
	}
	tests := []struct {
		version string
		config  map[string]any
		present []string
		absent  []string
	}{
		{
			version: "",
			config:  map[string]any{"disableAiter": true, "enableFlashAttention": true},
			present: append([]string{"VLLM_ROCM_USE_AITER_PAGED_ATTN", "VLLM_ROCM_USE_AITER_FP4_ASM_GEMM", "VLLM_USE_TRITON_FLASH_ATTN"}, disabled...),
		},
		{
			version: "0.26.0",
			config:  map[string]any{"disableAiter": true},
			present: append([]string{"VLLM_ROCM_USE_AITER_PAGED_ATTN", "VLLM_ROCM_USE_AITER_FP4_ASM_GEMM"}, disabled...),
		},
		{
			version: "0.32.0",
			config:  map[string]any{"disableAiter": true, "enableFlashAttention": true},
			present: append([]string{"VLLM_ROCM_USE_AITER_FP4_ASM_GEMM"}, disabled...),
			absent:  []string{"VLLM_ROCM_USE_AITER_PAGED_ATTN", "VLLM_USE_TRITON_FLASH_ATTN"},
		},
		{
			version: "0.33.0",
			config:  map[string]any{"rocmUseAiter": false},
			present: disabled,
			absent:  []string{"VLLM_ROCM_USE_AITER_PAGED_ATTN", "VLLM_ROCM_USE_AITER_FP4_ASM_GEMM"},
		},
		{
			version: "0.32.0",
			config:  map[string]any{"rocmCustomPagedAttn": false},
			absent:  []string{"VLLM_ROCM_USE_AITER_PAGED_ATTN"},
		},
	}
	for _, tt := range tests {
		t.Run("version="+tt.version, func(t *testing.T) {
			cfg := map[string]any{"failOnEnvironValidation": true}
			for k, v := range tt.config {
				cfg[k] = v
			}
			if tt.version != "" {
				cfg["vllmVersion"] = tt.version
			}
			env := (&VLLMBackend{}).Env(&ModelSpec{GPUVendor: GPUVendorAMD, GPUArch: "gfx1100", Config: cfg})
			names := map[string]int{}
			for _, e := range env {
				names[e.Name]++
			}
			for _, name := range tt.present {
				if names[name] != 1 {
					t.Errorf("%s appears %d times, want once", name, names[name])
				}
			}
			for _, name := range tt.absent {
				if names[name] != 0 {
					t.Errorf("%s rendered for vLLM %s, whose envs.py does not define it", name, tt.version)
				}
			}
		})
	}
}

// vllm033EnvironVariables are the VLLM_* names the backend can render, checked
// against vllm/envs.py at v0.33.0. A lane that declares 0.33.0 and sets
// failOnEnvironValidation must render nothing outside this set, or
// Platform.validate_environ refuses to start it. A new VLLM_* variable in
// Env belongs here only after it is checked against that file.
var vllm033EnvironVariables = []string{
	"VLLM_ALLOW_RUNTIME_LORA_UPDATING",
	"VLLM_DISABLED_KERNELS",
	"VLLM_ROCM_USE_AITER",
	"VLLM_ROCM_USE_AITER_FP4BMM",
	"VLLM_ROCM_USE_AITER_FP8BMM",
	"VLLM_ROCM_USE_AITER_FUSION_SHARED_EXPERTS",
	"VLLM_ROCM_USE_AITER_LINEAR",
	"VLLM_ROCM_USE_AITER_MHA",
	"VLLM_ROCM_USE_AITER_MLA",
	"VLLM_ROCM_USE_AITER_MOE",
	"VLLM_ROCM_USE_AITER_RMSNORM",
	"VLLM_ROCM_USE_AITER_TRITON_GEMM",
	"VLLM_ROCM_USE_AITER_TRITON_ROPE",
	"VLLM_ROCM_USE_AITER_UNIFIED_ATTENTION",
	"VLLM_SERVER_DEV_MODE",
}

func TestVLLMBackendEnv_DeclaredV033RendersOnlyDefinedVariables(t *testing.T) {
	for _, aiter := range []map[string]any{
		{"disableAiter": true},
		{"rocmUseAiter": true, "rocmCustomPagedAttn": true},
	} {
		cfg := map[string]any{
			"vllmVersion":             "0.33.0",
			"failOnEnvironValidation": true,
			"enableFlashAttention":    true,
			"disabledKernels":         "ExllamaLinearKernel",
			"enableLora":              true,
			"enableSleepMode":         true,
		}
		for k, v := range aiter {
			cfg[k] = v
		}
		for _, arch := range []string{"gfx1100", "gfx906"} {
			env := (&VLLMBackend{}).Env(&ModelSpec{GPUVendor: GPUVendorAMD, GPUArch: arch, Config: cfg})
			for _, e := range env {
				if strings.HasPrefix(e.Name, "VLLM_") && !slices.Contains(vllm033EnvironVariables, e.Name) {
					t.Errorf("%s with %v renders %s, which vllm/envs.py at v0.33.0 does not define", arch, aiter, e.Name)
				}
			}
		}
	}
}
