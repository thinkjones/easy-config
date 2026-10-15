// Package tui is ezc's terminal interface: four views over the store and the working repo, and the setup wizard.
//
// It imports no other ezc package. Everything it shows comes through the interfaces in Deps, which
// internal/cli implements by calling the functions the commands call, and every change it makes is an ezc command
// line run through Commands, shown on a confirm screen first: the TUI adds no behaviour of its own.
package tui

// Scope is which values the values view shows: the store's alone, or the working repo's over the store's.
type Scope int

const (
	StoreScope Scope = iota
	RepoScope
)

// Header is the top line: where the store is, which environment, and how the store stands against its remote.
type Header struct {
	Store                      string // the remote as it may be shown, else the store's folder
	Repo                       string // the working repo's ID; "" outside a repo
	Env                        string // the commands' default environment
	Ahead, Behind              int
	Diverged, Offline, Unknown bool // Unknown: the store's state cannot be told
	Requests                   int
}

// Value is one name of the values view.
type Value struct {
	Name, Type, Desc       string
	File                   string   // "store/common.yaml", "repo/env.local.enc.yaml"; "" when the name has no value
	Shown                  string   // the plaintext value; "" for a secret
	Secret                 bool     // declared secret, read from an encrypted file, or built from a secret
	Overrides              []string // base names of the files it overrides
	RequiredBy, OptionalIn []string // registered repos, from their checkouts on this machine
	Due                    string   // "readable by removed sam (2026-10-01)"; "" when not due
}

// Recipient is one entry of recipients.yaml.
type Recipient struct {
	Name, Kind, Owner, Added, AddedBy string
	Me                                bool // its key is this machine's
	Repos                             []string
}

// Request is one pending request for access. Problem is why it is not valid; "" when it is.
type Request struct{ Name, Kind, Owner, Host, Requested, Author, Key, Problem string }

// DueEntry is one secret that needs a new value. Secret is "<file>/<NAME>".
type DueEntry struct{ Secret, Removed, On string }

// AccessData is what the access view shows. Differ names the encrypted files not encrypted for exactly the registry.
type AccessData struct {
	Recipients []Recipient
	Requests   []Request
	Due        []DueEntry
	Differ     []string
}

// Repo is one registered repo and, when it has a checkout here, its check for the environment.
type Repo struct {
	ID, Checkout string
	Here, OK     bool
	Report       []string
}

// State is a doctor item's state.
type State int

const (
	OK State = iota
	Warn
	Broken
)

// Item is one line of the doctor's report.
type Item struct {
	State              State
	Label, Detail, Fix string
}

// Section is a group of the doctor's items: machine, store or repo.
type Section struct {
	Name  string
	Items []Item
}

// Outcome is what a command printed, in order, and whether it succeeded.
type Outcome struct {
	Lines []string
	OK    bool
}

// Store reads the header.
type Store interface{ Status() (Header, error) }

// Values reads the values view and reveals one value.
type Values interface {
	List(s Scope, env string) ([]Value, error)
	Reveal(s Scope, name, env string) (string, error)
}

// Access reads the access view.
type Access interface{ Load() (AccessData, error) }

// Repos reads the repos view, each repo checked for env.
type Repos interface {
	Load(env string) ([]Repo, error)
}

// Doctor reads the doctor's report for env.
type Doctor interface {
	Check(env string) ([]Section, error)
}

// Commands runs one ezc command line (without "ezc") with stdin, and is the only way the TUI changes anything.
type Commands interface {
	Run(args []string, stdin string) Outcome
}

// Deps is everything the TUI needs from ezc.
type Deps struct {
	Store    Store
	Values   Values
	Access   Access
	Repos    Repos
	Doctor   Doctor
	Commands Commands
}
