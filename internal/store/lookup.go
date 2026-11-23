package store

// LookupTables are the only tables whose rows are not scoped to one org
// (ADR-0002 decision 9). They hold a lookup key, mapped ids, an expiry and a
// revoked or used marker, never a secret. They are read before an org is known.
var LookupTables = []string{
	"user_identities",
	"user_orgs",
	"agent_keys",
	"enrolment_token_hashes",
	"token_hashes",
	"session_hashes",
	"link_token_hashes",
}
