package wsl

import (
	"testing"
)

func TestConvertWindowsPathToWSL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "C drive path",
			input:    "C:\\Users\\fish\\test.py",
			expected: "/mnt/c/Users/fish/test.py",
		},
		{
			name:     "D drive path",
			input:    "D:\\Project\\code.py",
			expected: "/mnt/d/Project/code.py",
		},
		{
			name:     "Lowercase drive",
			input:    "c:\\windows\\system32",
			expected: "/mnt/c/windows/system32",
		},
		{
			name:     "Already forward slashes",
			input:    "C:/Users/fish/test.py",
			expected: "/mnt/c/Users/fish/test.py",
		},
		{
			name:     "Relative path",
			input:    "test.py",
			expected: "test.py",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertWindowsPathToWSL(tt.input)
			if result != tt.expected {
				t.Errorf("ConvertWindowsPathToWSL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
