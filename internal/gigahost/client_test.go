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
		_, _ = w.Write([]byte(`{"meta":{},"data":[
			{"srv_id":"42","srv_status":"0","srv_status_install":"1","srv_date_created":"1781282056","srv_deleted_date":"0","order":{"order_id":"100","order_status":"active"}},
			{"srv_id":"43","srv_status":"1","srv_status_install":"0","srv_date_created":"1781280000","srv_deleted_date":"1781283000","order":{"order_id":"101","order_status":"Cancelled"}}
		]}`))
	}))
	defer srv.Close()

	c, err := NewClient(&Config{Address: srv.URL + "/api/v0", Token: "test-token"})
	must.NoError(t, err)

	servers, err := c.ListServers(context.Background())
	must.NoError(t, err)
	must.Eq(t, 2, len(servers))
	must.Eq(t, "42", servers[0].SrvID)
	must.True(t, servers[0].Installing())
	must.False(t, servers[0].Running())
	must.False(t, servers[0].Cancelled())
	must.Eq(t, "100", servers[0].Order.OrderID)
	must.Eq(t, int64(1781282056), servers[0].Created())
	must.Eq(t, int64(0), servers[0].Deleted())
	must.False(t, servers[1].Installing())
	must.True(t, servers[1].Running())
	must.True(t, servers[1].Cancelled())
	must.Eq(t, int64(1781283000), servers[1].Deleted())
}

func TestClientGetServer(t *testing.T) {
	t.Run("found (array-wrapped)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			must.Eq(t, "/api/v0/servers/42", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"meta":{},"data":[{"srv_id":"42","srv_status":"1","order":{"order_status":"active"}}]}`))
		}))
		defer srv.Close()

		c, err := NewClient(&Config{Address: srv.URL + "/api/v0", Token: "test-token"})
		must.NoError(t, err)

		s, err := c.GetServer(context.Background(), "42")
		must.NoError(t, err)
		must.Eq(t, "42", s.SrvID)
		must.True(t, s.Running())
	})

	t.Run("404 is ErrNotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"meta":{"message":"404 Not Found"},"data":[]}`))
		}))
		defer srv.Close()

		c, err := NewClient(&Config{Address: srv.URL + "/api/v0", Token: "test-token"})
		must.NoError(t, err)

		_, err = c.GetServer(context.Background(), "42")
		must.Error(t, err)
		must.True(t, errors.Is(err, ErrNotFound))
	})

	// An empty or non-numeric id must be rejected before the request, or the
	// path collapses to the list endpoint and answers with another server.
	t.Run("invalid id rejected without a request", func(t *testing.T) {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))
		defer srv.Close()

		c, err := NewClient(&Config{Address: srv.URL + "/api/v0", Token: "test-token"})
		must.NoError(t, err)

		for _, id := range []string{"", "  ", "abc"} {
			_, err = c.GetServer(context.Background(), id)
			must.Error(t, err)
		}
		must.False(t, called)
	})
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

func TestClientDeployQuantity(t *testing.T) {
	cases := map[string]struct {
		quantity int64
		want     float64
	}{
		"explicit batch":   {quantity: 3, want: 3},
		"zero defaults to": {quantity: 0, want: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				must.Eq(t, "/api/v0/deploy/servers", r.URL.Path)
				must.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"meta":{},"data":{"order_ids":[1,2,3]}}`))
			}))
			defer srv.Close()

			c, err := NewClient(&Config{Address: srv.URL + "/api/v0", Token: "test-token"})
			must.NoError(t, err)

			result, err := c.Deploy(context.Background(), DeployInput{
				ProductID: 1, PriceID: 2, RegionID: 3, Quantity: tc.quantity,
			})
			must.NoError(t, err)
			q, ok := body["quantity"].(float64)
			must.True(t, ok)
			must.Eq(t, tc.want, q)
			must.Eq(t, 3, len(result.OrderIDs))
		})
	}
}
