package browse

import (
	"slices"
	"testing"
)

func TestFilterExtensions(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{[]string{"bin", ".s19", "*.hex", "tar.gz"}, []string{"bin", "s19", "hex", "tar.gz"}},
		{nil, nil},
		{[]string{"bin", "*"}, nil},
		{[]string{"*.*"}, nil},
		{[]string{""}, nil},
	}
	for _, tt := range tests {
		if got := (Filter{Extensions: tt.in}).extensions(); !slices.Equal(got, tt.want) {
			t.Errorf("extensions(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFilterLabel(t *testing.T) {
	tests := []struct {
		f    Filter
		want string
	}{
		{Filter{Name: "Binary", Extensions: []string{"bin"}}, "Binary"},
		{Filter{Extensions: []string{"bin", "s19"}}, "*.bin, *.s19"},
		{Filter{}, "*"},
	}
	for _, tt := range tests {
		if got := tt.f.label(); got != tt.want {
			t.Errorf("label(%+v) = %q, want %q", tt.f, got, tt.want)
		}
	}
}
