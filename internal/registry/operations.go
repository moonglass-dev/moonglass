package registry

import "moonglass.dev/moonglass/internal/store"

// ServersListIn is the (empty) input of servers.list.
type ServersListIn struct{}

// ServerInfo is one enrolled server.
type ServerInfo struct {
	ID        string `json:"id" doc:"Server id"`
	CreatedAt string `json:"created_at" doc:"When the server enrolled, RFC 3339"`
}

// ServersListOut is the output of servers.list.
type ServersListOut struct {
	Servers []ServerInfo `json:"servers" doc:"Servers enrolled in the caller's org"`
}

var serversListSpec = Spec{
	Name:        "servers.list",
	Summary:     "List servers",
	Method:      "GET",
	Path:        "/v1/servers",
	MinRole:     store.RoleViewer,
	Destructive: No,
}

// Operations returns the declaration of every shipped operation. REST and CLI
// generation read this list.
func Operations() []Spec {
	return []Spec{serversListSpec}
}
