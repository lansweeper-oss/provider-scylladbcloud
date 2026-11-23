package config

import (
	"reflect"
	"testing"
)

func TestCQLConnectionDetails(t *testing.T) {
	tests := map[string]struct {
		attr map[string]any
		want map[string][]byte
	}{
		"publishes every key": {
			attr: map[string]any{
				"cql_username":   "scylla",
				"cql_password":   "secret",
				"ca_certificate": "PEM",
				"node_dns_names": []any{"node-0.example", "", "node-1.example"},
			},
			want: map[string][]byte{
				"username": []byte("scylla"),
				"password": []byte("secret"),
				"ca.crt":   []byte("PEM"),
				"seeds":    []byte("node-0.example,node-1.example"),
			},
		},
		"skips empty and missing values": {
			attr: map[string]any{"cql_username": "scylla", "cql_password": "", "node_dns_names": []any{""}},
			want: map[string][]byte{"username": []byte("scylla")},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := cqlConnectionDetails(tt.attr)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("want %q, got %q (error %v)", tt.want, got, err)
			}
		})
	}
}
