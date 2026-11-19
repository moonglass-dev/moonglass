package deploy

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCheckIssuerAcceptsTheThreeIssuers(t *testing.T) {
	tests := []struct {
		name   string
		issuer string
	}{
		{"acme", "acme"},
		{"acme staging", "acme-staging"},
		{"internal", "internal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckIssuer(tc.issuer); err != nil {
				t.Errorf("CheckIssuer(%q) = %v, want nil", tc.issuer, err)
			}
		})
	}
}

func TestCheckIssuerRefusesOtherValues(t *testing.T) {
	tests := []struct {
		name   string
		issuer string
	}{
		{"empty", ""},
		{"letsencrypt", "letsencrypt"},
		{"upper case", "ACME"},
		{"leading space", " acme"},
		{"trailing space", "acme-staging "},
		{"mixed case", "Internal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckIssuer(tc.issuer)
			if err == nil {
				t.Fatalf("CheckIssuer(%q) = nil, want an error", tc.issuer)
			}
			for _, want := range []string{fmt.Sprintf("%q", tc.issuer), "acme", "acme-staging", "internal"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("CheckIssuer(%q) error = %q, want it to contain %q", tc.issuer, err, want)
				}
			}
		})
	}
}

func TestCheckIssuerAgreesWithPush(t *testing.T) {
	tests := []struct {
		name   string
		issuer string
	}{
		{"acme", "acme"},
		{"acme staging", "acme-staging"},
		{"internal", "internal"},
		{"letsencrypt", "letsencrypt"},
		{"upper case", "ACME"},
		{"empty", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			fake := newFakeAdmin(t)
			pusher := NewProxy(nil, newProxyDB(t), ProxyConfig{Socket: fake.path, Issuer: tc.issuer})

			pushErr := pusher.Push(ctx)
			checkErr := CheckIssuer(tc.issuer)

			if (pushErr == nil) != (checkErr == nil) {
				t.Fatalf("Push() with issuer %q error = %v, CheckIssuer(%q) = %v; want both nil or both an error",
					tc.issuer, pushErr, tc.issuer, checkErr)
			}
			if checkErr == nil {
				return
			}
			if pushErr.Error() != checkErr.Error() {
				t.Errorf("Push() with issuer %q error = %q, want CheckIssuer's error %q", tc.issuer, pushErr, checkErr)
			}
			requireEqual(t, "requests on the admin socket", fake.requestCount(), 0)
		})
	}
}
