package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLabException(t *testing.T) {
	app := &application{}
	request := httptest.NewRequest(http.MethodPost, "/api/lab/exception", nil)
	response := httptest.NewRecorder()
	app.labException(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unguarded exception status = %d", response.Code)
	}

	request.Header.Set("X-Lab-Action", "1")
	response = httptest.NewRecorder()
	app.labException(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("recovered exception status = %d", response.Code)
	}
}

func TestParseAmount(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{name: "whole", input: "10", want: 1000},
		{name: "one decimal", input: "10.5", want: 1050},
		{name: "two decimals", input: "49.99", want: 4999},
		{name: "leading and trailing space", input: " 7.25 ", want: 725},
		{name: "too many decimals", input: "1.001", wantErr: true},
		{name: "negative", input: "-1.00", wantErr: true},
		{name: "not numeric", input: "abc", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseAmount(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseAmount(%q) returned no error", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAmount(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("parseAmount(%q) = %d, want %d", test.input, got, test.want)
			}
		})
	}
}

func TestNewUUIDShapeAndVersion(t *testing.T) {
	id := newUUID()
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("newUUID() returned malformed UUID %q", id)
	}
	if id[14] != '4' {
		t.Fatalf("newUUID() returned UUID %q without version 4", id)
	}
}
