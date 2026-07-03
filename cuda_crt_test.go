package main

import "testing"

func TestCudaVersionFromAsset(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "llama-server-bin-win-cuda-cu12.4-x64.7z",
			want: "12.4",
		},
		{
			name: "llama-b9860-bin-win-cuda-12.4-x64.zip",
			want: "12.4",
		},
		{
			name: "llama-b9860-bin-win-avx2-x64.zip",
			want: "",
		},
	}

	for _, tt := range tests {
		if got := cudaVersionFromAsset(tt.name); got != tt.want {
			t.Errorf("cudaVersionFromAsset(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestFindMatchingCudaCrt(t *testing.T) {
	assets := []ghReleaseAsset{
		{Name: "llama-b9860-bin-win-cuda-12.4-x64.zip"},
		{Name: "cudart-llama-bin-win-cuda-12.2-x64.zip"},
		{Name: "CUDART-llama-bin-win-cuda-12.4-x64.zip"},
	}

	got := findMatchingCudaCrt(assets, "llama-b9860-bin-win-cuda-12.4-x64.zip")
	if got == nil {
		t.Fatal("expected matching CUDA CRT asset")
	}
	if got.Name != "CUDART-llama-bin-win-cuda-12.4-x64.zip" {
		t.Errorf("expected 12.4 CUDA CRT asset, got %q", got.Name)
	}
}
