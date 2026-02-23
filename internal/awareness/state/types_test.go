package state

import (
	"testing"
)

func TestStateTransitionHasChanged(t *testing.T) {
	transition := StateTransition{
		ChangedFields: []string{"online", "context", "ipv4"},
	}

	tests := []struct {
		field string
		want  bool
	}{
		{"online", true},
		{"context", true},
		{"ipv4", true},
		{"location", false},
		{"ipv6", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			got := transition.HasChanged(tt.field)
			if got != tt.want {
				t.Errorf("HasChanged(%q) = %v, want %v", tt.field, got, tt.want)
			}
		})
	}
}

func TestStateTransitionHasChangedEmpty(t *testing.T) {
	transition := StateTransition{}

	if transition.HasChanged("online") {
		t.Error("Expected HasChanged to return false for empty ChangedFields")
	}
}

func TestLogLevelString(t *testing.T) {
	tests := []struct {
		level LogLevel
		want  string
	}{
		{LogDebug, "DEBUG"},
		{LogInfo, "INFO"},
		{LogWarn, "WARN"},
		{LogError, "ERROR"},
		{LogLevel(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.level.String()
			if got != tt.want {
				t.Errorf("LogLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

func TestLogCategoryString(t *testing.T) {
	tests := []struct {
		cat  LogCategory
		want string
	}{
		{CategorySensor, "sensor"},
		{CategoryState, "state"},
		{CategoryEffect, "effect"},
		{CategorySystem, "system"},
		{CategoryHook, "hook"},
		{LogCategory(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.cat.String()
			if got != tt.want {
				t.Errorf("LogCategory(%d).String() = %q, want %q", tt.cat, got, tt.want)
			}
		})
	}
}

func TestLogCategoryIcon(t *testing.T) {
	tests := []struct {
		cat  LogCategory
		want string
	}{
		{CategorySensor, "~"},
		{CategoryState, "*"},
		{CategoryEffect, ">"},
		{CategorySystem, "#"},
		{CategoryHook, "!"},
		{LogCategory(99), "?"},
	}

	for _, tt := range tests {
		t.Run(tt.cat.String(), func(t *testing.T) {
			got := tt.cat.Icon()
			if got != tt.want {
				t.Errorf("LogCategory(%d).Icon() = %q, want %q", tt.cat, got, tt.want)
			}
		})
	}
}
