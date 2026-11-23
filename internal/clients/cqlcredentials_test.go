package clients

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/scylladb/terraform-provider-scylladbcloud/pkg/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestAddCQLCredentials(t *testing.T) {
	const connectURI = "/account/0/cluster/connect?clusterId=42"
	tests := map[string]struct {
		readErr      bool
		prevPassword string
		status       int
		body         string
		wantCalls    []string
		wantUsername string
		wantPassword string
		wantDiags    int
	}{
		"fetches the credentials": {
			status: http.StatusOK, body: `{"data":{"credentials":{"username":"scylla","password":"secret"}}}`,
			wantCalls: []string{connectURI}, wantUsername: "scylla", wantPassword: "secret",
		},
		"keeps credentials already fetched": {prevPassword: "old", wantPassword: "old"},
		"skips a failed read":               {readErr: true, wantDiags: 1},
		"downgrades an API failure to a warning": {
			status: http.StatusInternalServerError, body: `{"error":"000000"}`,
			wantCalls: []string{connectURI}, wantDiags: 1,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := &schema.Resource{
				Schema: map[string]*schema.Schema{"cluster_id": {Type: schema.TypeInt, Computed: true}},
				ReadContext: func(_ context.Context, d *schema.ResourceData, _ any) diag.Diagnostics {
					if tt.readErr {
						return diag.Errorf("read failed")
					}
					d.SetId("42")
					_ = d.Set("cluster_id", 42)
					return nil
				},
			}
			provider.AddCQLCredentials(r)
			var calls []string
			meta, err := provider.NewOfflineClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.URL.RequestURI())
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Request: req}, nil
			}))
			if err != nil {
				t.Fatalf("cannot build the offline client: %v", err)
			}
			d := r.TestResourceData()
			_ = d.Set("cql_password", tt.prevPassword)

			diags := r.ReadContext(context.Background(), d, meta)

			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("API calls: want %q, got %q", tt.wantCalls, calls)
			}
			if u, p := d.Get("cql_username"), d.Get("cql_password"); u != tt.wantUsername || p != tt.wantPassword {
				t.Errorf("credentials: want %q/%q, got %q/%q", tt.wantUsername, tt.wantPassword, u, p)
			}
			if len(diags) != tt.wantDiags || diags.HasError() != tt.readErr {
				t.Errorf("want %d diagnostics, error %t; got %v", tt.wantDiags, tt.readErr, diags)
			}
		})
	}
}

// The provider every caller builds must carry the credentials, and only the
// password may be hidden from the managed resource's status.
func TestNewAddsCQLCredentialsToCluster(t *testing.T) {
	s := provider.New(context.Background()).ResourcesMap["scylladbcloud_cluster"].Schema
	for attr, sensitive := range map[string]bool{"cql_username": false, "cql_password": true} {
		if sch := s[attr]; sch == nil || !sch.Computed || sch.Optional || sch.Sensitive != sensitive {
			t.Errorf("%s: want computed-only, sensitive %t; got %+v", attr, sensitive, sch)
		}
	}
}
