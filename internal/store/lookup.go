package store

// LookupTables are the only tables without org_id row-level security
// (ADR-0002 decision 12). They hold a lookup key, mapped ids, an expiry and a
// revoked or used marker, never a secret. They are read before an org is known.
var LookupTables = []string{
	"user_identities",
	"user_orgs",
	"agent_keys",
	"enrolment_token_hashes",
	"token_hashes",
}
