package registry_test

import (
	"testing"

	"moonglass.dev/moonglass/internal/registry"
)

// TestShippedOperationsKeepSpecOrder checks that the six operations of W2-11d
// are declared after servers.list and in the order the spec's table gives.
// REST and CLI generation read the declarations in this order.
func TestShippedOperationsKeepSpecOrder(t *testing.T) {
	want := []string{
		"servers.list",
		"projects.create",
		"projects.list",
		"environments.create",
		"environments.list",
		"services.create",
		"services.list",
	}
	position := make(map[string]int)
	for i, d := range registry.Operations() {
		position[d.Name] = i
	}
	for i := 1; i < len(want); i++ {
		prev, ok := position[want[i-1]]
		if !ok {
			t.Fatalf("registry.Operations() does not declare %q, want it before %q", want[i-1], want[i])
		}
		got, ok := position[want[i]]
		if !ok {
			t.Fatalf("registry.Operations() does not declare %q, want it after %q", want[i], want[i-1])
		}
		if got <= prev {
			t.Errorf("registry.Operations() declares %q at %d and %q at %d, want %q after %q",
				want[i-1], prev, want[i], got, want[i], want[i-1])
		}
	}
}
