package cmd

import (
	"testing"
)

func TestFormatEnvInfo(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "nil environment",
			env:  nil,
			want: "",
		},
		{
			name: "empty environment",
			env:  map[string]string{},
			want: "",
		},
		{
			name: "only OVERSEER_ variables",
			env: map[string]string{
				"OVERSEER_CONTEXT":   "home",
				"OVERSEER_LOCATION":  "living-room",
				"OVERSEER_PUBLIC_IP": "1.2.3.4",
			},
			want: "",
		},
		{
			name: "single user variable",
			env: map[string]string{
				"MY_VAR": "hello",
			},
			want: " \033[2m[MY_VAR=hello]\033[0m",
		},
		{
			name: "multiple user variables sorted",
			env: map[string]string{
				"ZOO":  "zebra",
				"ALPHA": "first",
			},
			want: " \033[2m[ALPHA=first, ZOO=zebra]\033[0m",
		},
		{
			name: "mixed user and OVERSEER_ variables",
			env: map[string]string{
				"OVERSEER_CONTEXT": "office",
				"MY_VAR":          "value",
				"OVERSEER_TAG":    "abc",
				"OTHER":           "stuff",
			},
			want: " \033[2m[MY_VAR=value, OTHER=stuff]\033[0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatEnvInfo(tt.env)
			if got != tt.want {
				t.Errorf("formatEnvInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}
