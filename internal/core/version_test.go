package core

import "testing"

func TestFormatVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "tagged release with v prefix",
			input: "v1.12.0",
			want:  "1.12.0",
		},
		{
			name:  "tagged release without v prefix",
			input: "1.12.0",
			want:  "1.12.0",
		},
		{
			name:  "devel with sha",
			input: "devel-ad721b3",
			want:  "devel-ad721b3",
		},
		{
			name:  "devel with sha dirty",
			input: "devel-ad721b3-dirty",
			want:  "devel-ad721b3-dirty",
		},
		{
			name:  "plain devel",
			input: "devel",
			want:  "devel",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatVersion(tt.input)
			if got != tt.want {
				t.Errorf("FormatVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsPseudoVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "pseudo-version without tag",
			input: "v0.0.0-20260217105831-82903d1d8810",
			want:  true,
		},
		{
			name:  "pseudo-version with dirty",
			input: "v0.0.0-20260217105831-82903d1d8810+dirty",
			want:  true,
		},
		{
			name:  "pseudo-version based on tag",
			input: "v1.12.1-0.20260217105831-82903d1d8810",
			want:  true,
		},
		{
			name:  "tagged release",
			input: "v1.12.0",
			want:  false,
		},
		{
			name:  "prerelease version",
			input: "v2.0.0-rc1",
			want:  false,
		},
		{
			name:  "devel",
			input: "(devel)",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPseudoVersion(tt.input)
			if got != tt.want {
				t.Errorf("isPseudoVersion(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
