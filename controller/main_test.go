package main

import "testing"

func TestValidAPIVersion(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "1.44", want: true},
		{value: "1.52", want: true},
		{value: "", want: false},
		{value: "v1.44", want: false},
		{value: "1", want: false},
		{value: "1.44.0", want: false},
		{value: "latest", want: false},
	}

	for _, test := range tests {
		if got := validAPIVersion(test.value); got != test.want {
			t.Errorf("validAPIVersion(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}
