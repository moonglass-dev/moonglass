package registry

// Route is an HTTP route that is not an operation, with the authorisation it enforces.
type Route struct {
	Method        string
	Path          string
	Authorisation string
}

// NonOperationRoutes is the fixed list of routes outside the registry.
var NonOperationRoutes = []Route{
	{"GET", "/v1/agent", "Ed25519 challenge-response with an enrolled agent key, on the agent listener (docs/specs/connection.md)"},
	{"POST", "/v1/enroll", "single-use enrolment token as a Bearer value (docs/specs/enrolment.md)"},
}

// LocalCommand is a CLI subcommand that runs on this machine and is not an operation.
type LocalCommand struct {
	Name     string
	Synopsis string
	Summary  string
	Touches  string
}

// LocalCommands is the fixed list of local-only subcommands.
var LocalCommands = []LocalCommand{
	{"version", "version", "Print the version, commit and build date", "nothing"},
	{"server", "server [--data-dir DIR] [--listen ADDR] [--agent-listen ADDR] [--print-enroll-token]", "Run the control plane", "the server data dir and its listeners"},
	{"agent enroll", "agent enroll [--data-dir DIR] --url URL TOKEN", "Enrol this machine and print its fingerprint", "the agent data dir and the control-plane enrolment route"},
	{"agent connect", "agent connect [--data-dir DIR] --url URL", "Connect to the control plane over WSS and send heartbeats", "the agent data dir and the control-plane agent listener"},
	{"proxy", "proxy --data-dir DIR", "Run the proxy (Caddy) with its admin API on a Unix socket in DIR", "the proxy data dir and its admin socket"},
}
