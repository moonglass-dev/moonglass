package registry_test

import (
	"reflect"
	"testing"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
)

// TestOperationsListInTypes checks that every shipped operation is declared
// with the type of its input, which generation reads, and that the one live
// operation's Spec is unchanged.
func TestOperationsListInTypes(t *testing.T) {
	ops := registry.Operations()
	if len(ops) == 0 {
		t.Fatalf("registry.Operations() = 0 entries, want at least the shipped servers.list")
	}

	names := make([]string, 0, len(ops))
	for _, d := range ops {
		names = append(names, d.Name)
		if d.In == nil {
			t.Errorf("operation %q has In = nil, want the reflect.Type of its input struct", d.Name)
			continue
		}
		if d.In.Kind() != reflect.Struct {
			t.Errorf("operation %q has In = %s of kind %s, want a struct type", d.Name, d.In, d.In.Kind())
		}
	}

	wantSpec := registry.Spec{
		Name:        "servers.list",
		Summary:     "List servers",
		Method:      "GET",
		Path:        "/v1/servers",
		MinRole:     store.RoleViewer,
		Destructive: registry.No,
	}
	var found bool
	for _, d := range ops {
		if d.Name != wantSpec.Name {
			continue
		}
		found = true
		if d.Spec != wantSpec {
			t.Errorf("servers.list Spec = %+v, want %+v", d.Spec, wantSpec)
		}
		if want := reflect.TypeFor[registry.ServersListIn](); d.In != want {
			t.Errorf("servers.list In = %v, want %v", d.In, want)
		}
	}
	if !found {
		t.Errorf("registry.Operations() names %v, want servers.list among them", names)
	}
}
