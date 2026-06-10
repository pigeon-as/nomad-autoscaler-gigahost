// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package gigahost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shoenig/test/must"
)

func TestFlexInt64UnmarshalJSON(t *testing.T) {
	cases := map[string]struct {
		in   string
		want int64
	}{
		"string number": {`"123"`, 123},
		"bare number":   {`456`, 456},
		"empty string":  {`""`, 0},
		"null":          {`null`, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var v flexInt64
			must.NoError(t, json.Unmarshal([]byte(tc.in), &v))
			must.Eq(t, tc.want, int64(v))
		})
	}
}

func TestFlexBoolUnmarshalJSON(t *testing.T) {
	cases := map[string]struct {
		in   string
		want bool
	}{
		"one":          {`"1"`, true},
		"zero":         {`"0"`, false},
		"true":         {`"true"`, true},
		"empty string": {`""`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var v flexBool
			must.NoError(t, json.Unmarshal([]byte(tc.in), &v))
			must.Eq(t, tc.want, bool(v))
		})
	}
}

func TestClientListServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		must.Eq(t, "/api/v0/servers", r.URL.Path)
		must.Eq(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":{},"data":[{"srv_id":"42","srv_name":"worker-1"}]}`))
	}))
	defer srv.Close()

	c, err := NewClient(&Config{Address: srv.URL + "/api/v0", Token: "test-token"})
	must.NoError(t, err)

	servers, err := c.ListServers(context.Background())
	must.NoError(t, err)
	must.Eq(t, 1, len(servers))
	must.Eq(t, "42", servers[0].SrvID)
}

func TestClientCancelServerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"meta":{"message":"not found"},"data":null}`))
	}))
	defer srv.Close()

	c, err := NewClient(&Config{Address: srv.URL + "/api/v0", Token: "test-token"})
	must.NoError(t, err)

	err = c.CancelServer(context.Background(), "999")
	must.Error(t, err)
	must.True(t, errors.Is(err, ErrNotFound))
}

func TestNewClientRequiresToken(t *testing.T) {
	_, err := NewClient(&Config{})
	must.Error(t, err)
}
