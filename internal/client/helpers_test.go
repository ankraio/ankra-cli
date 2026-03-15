package client

import (
	"net/http"
	"testing"
)

func TestGetJSON(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		wantErr        bool
		validateResult func(t *testing.T, target interface{})
	}{
		{
			name: "200 with valid JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(t, w, http.StatusOK, map[string]string{"key": "value"})
			},
			wantErr: false,
			validateResult: func(t *testing.T, target interface{}) {
				m, ok := target.(*map[string]string)
				if !ok || m == nil {
					t.Fatal("target should be *map[string]string")
				}
				if (*m)["key"] != "value" {
					t.Errorf("got %v, want value", (*m)["key"])
				}
			},
		},
		{
			name: "401 unauthorized",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErr:        true,
			validateResult: nil,
		},
		{
			name: "non-200 status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErr:        true,
			validateResult: nil,
		},
		{
			name: "malformed JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("not valid json"))
			},
			wantErr:        true,
			validateResult: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := newTestClient(t, tt.handler)
			url := testClient.BaseURL + "/api/test"
			var target map[string]string
			err := testClient.getJSON(url, &target)
			if (err != nil) != tt.wantErr {
				t.Errorf("getJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.validateResult != nil {
				tt.validateResult(t, &target)
			}
		})
	}
}

func TestParseJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		target  interface{}
		wantErr bool
	}{
		{
			name:    "valid JSON",
			data:    []byte(`{"name":"test","value":42}`),
			target:  &struct{ Name string `json:"name"`; Value int `json:"value"` }{},
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			data:    []byte(`{invalid json}`),
			target:  &struct{}{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseJSON(tt.data, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
