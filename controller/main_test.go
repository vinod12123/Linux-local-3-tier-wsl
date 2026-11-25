package main

import "testing"

func TestActionObserved(t *testing.T) {
	cleanExit, crashExit := 0, 17
	for _, tc := range []struct {
		name, action string
		state        containerState
		want         bool
	}{
		{"crash still running", "crash", containerState{Running: true, Status: "running"}, false},
		{"clean exit is not a crash", "crash", containerState{Status: "exited", Exit: &cleanExit}, false},
		{"nonzero exit confirms crash", "crash", containerState{Status: "exited", Exit: &crashExit}, true},
		{"clean exit confirms stop", "stop", containerState{Status: "exited", Exit: &cleanExit}, true},
		{"starting is not restored", "restore", containerState{Running: true, Health: "starting"}, false},
		{"healthy confirms restore", "restore", containerState{Running: true, Health: "healthy"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := actionObserved(tc.state, tc.action); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

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
