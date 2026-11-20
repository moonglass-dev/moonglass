package registry

import (
	"reflect"

	"moonglass.dev/moonglass/internal/store"
)

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

// ProjectsCreateIn is the input of projects.create.
type ProjectsCreateIn struct {
	Name string `json:"name" doc:"Project name"`
}

// ProjectsListIn is the (empty) input of projects.list.
type ProjectsListIn struct{}

// EnvironmentsCreateIn is the input of environments.create.
type EnvironmentsCreateIn struct {
	Project string `json:"project" doc:"Project name"`
	Name    string `json:"name" doc:"Environment name"`
}

// EnvironmentsListIn is the input of environments.list.
type EnvironmentsListIn struct {
	Project string `json:"project" doc:"Project name"`
}

// ServicesCreateIn is the input of services.create.
type ServicesCreateIn struct {
	Project     string `json:"project" doc:"Project name"`
	Environment string `json:"environment" doc:"Environment name"`
	Name        string `json:"name" doc:"Service name"`
	Server      string `json:"server" doc:"Server id of the caller's org"`
}

// ServicesListIn is the input of services.list.
type ServicesListIn struct {
	Project     string `json:"project" doc:"Project name"`
	Environment string `json:"environment" doc:"Environment name"`
}

// ProjectInfo is one project row.
type ProjectInfo struct {
	ID        string `json:"id" doc:"Project id"`
	Name      string `json:"name" doc:"Project name"`
	CreatedAt string `json:"created_at" doc:"When the project was created, RFC 3339"`
}

// EnvironmentInfo is one environment row.
type EnvironmentInfo struct {
	ID        string `json:"id" doc:"Environment id"`
	Name      string `json:"name" doc:"Environment name"`
	CreatedAt string `json:"created_at" doc:"When the environment was created, RFC 3339"`
}

// ServiceInfo is one service row.
type ServiceInfo struct {
	ID        string `json:"id" doc:"Service id"`
	Name      string `json:"name" doc:"Service name"`
	Server    string `json:"server" doc:"Server id the service runs on"`
	CreatedAt string `json:"created_at" doc:"When the service was created, RFC 3339"`
}

// ProjectsListOut is the output of projects.list: the org's projects in name
// order, empty and never null when there are none.
type ProjectsListOut struct {
	Projects []ProjectInfo `json:"projects" doc:"Projects of the caller's org"`
}

// EnvironmentsListOut is the output of environments.list: the project's
// environments in name order, empty and never null when there are none.
type EnvironmentsListOut struct {
	Environments []EnvironmentInfo `json:"environments" doc:"Environments of the project"`
}

// ServicesListOut is the output of services.list: the environment's services in
// name order, empty and never null when there are none.
type ServicesListOut struct {
	Services []ServiceInfo `json:"services" doc:"Services of the environment"`
}

// Declaration is an operation's Spec and the type of its input; REST and CLI
// generation read it.
type Declaration struct {
	In reflect.Type
	Spec
}

var serversListSpec = Spec{
	Name:        "servers.list",
	Summary:     "List servers",
	Method:      "GET",
	Path:        "/v1/servers",
	MinRole:     store.RoleViewer,
	Destructive: No,
}

var projectsCreateSpec = Spec{
	Name:        "projects.create",
	Summary:     "Create a project",
	Method:      "POST",
	Path:        "/v1/projects",
	MinRole:     store.RoleMember,
	Destructive: No,
}

var projectsListSpec = Spec{
	Name:        "projects.list",
	Summary:     "List projects",
	Method:      "GET",
	Path:        "/v1/projects",
	MinRole:     store.RoleViewer,
	Destructive: No,
}

var environmentsCreateSpec = Spec{
	Name:        "environments.create",
	Summary:     "Create an environment",
	Method:      "POST",
	Path:        "/v1/environments",
	MinRole:     store.RoleMember,
	Destructive: No,
}

var environmentsListSpec = Spec{
	Name:        "environments.list",
	Summary:     "List environments",
	Method:      "GET",
	Path:        "/v1/environments",
	MinRole:     store.RoleViewer,
	Destructive: No,
}

var servicesCreateSpec = Spec{
	Name:        "services.create",
	Summary:     "Create a service",
	Method:      "POST",
	Path:        "/v1/services",
	MinRole:     store.RoleMember,
	Destructive: No,
}

var servicesListSpec = Spec{
	Name:        "services.list",
	Summary:     "List services",
	Method:      "GET",
	Path:        "/v1/services",
	MinRole:     store.RoleViewer,
	Destructive: No,
}

// Operations returns the declaration of every shipped operation. REST and CLI
// generation read this list.
func Operations() []Declaration {
	return []Declaration{
		{Spec: serversListSpec, In: reflect.TypeFor[ServersListIn]()},
		{Spec: projectsCreateSpec, In: reflect.TypeFor[ProjectsCreateIn]()},
		{Spec: projectsListSpec, In: reflect.TypeFor[ProjectsListIn]()},
		{Spec: environmentsCreateSpec, In: reflect.TypeFor[EnvironmentsCreateIn]()},
		{Spec: environmentsListSpec, In: reflect.TypeFor[EnvironmentsListIn]()},
		{Spec: servicesCreateSpec, In: reflect.TypeFor[ServicesCreateIn]()},
		{Spec: servicesListSpec, In: reflect.TypeFor[ServicesListIn]()},
	}
}
