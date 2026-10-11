package recipients

import (
	"errors"
	"fmt"
	"maps"
	"strings"
)

// The kinds of recipient.
const (
	KindUser    = "user"
	KindMachine = "machine"
	KindBot     = "bot"
)

// ErrLast is returned when the only recipient would be removed.
var ErrLast = errors.New("the last recipient cannot be removed")

// OwnsError is returned when a user to remove is the owner of machines.
type OwnsError struct{ Machines []string }

func (e *OwnsError) Error() string {
	return "this user owns machines: " + strings.Join(e.Machines, ", ")
}

func (r Registry) clone() Registry {
	return Registry{Recipients: maps.Clone(r.Recipients)}
}

// keyFree reports an error naming the holder when key is registered under a name other than except.
func (r Registry) keyFree(key, except string) error {
	if holder := r.NameOf(key); holder != "" && holder != except {
		return fmt.Errorf("that key is already registered to %s", holder)
	}
	return nil
}

// With is a copy of the registry with the entry added.
func (r Registry) With(name string, rec Recipient) (Registry, error) {
	if !ValidName(name) {
		return Registry{}, fmt.Errorf("%q is not a valid recipient name (lower case letters, digits and -)", name)
	}
	if _, taken := r.Recipients[name]; taken {
		return Registry{}, fmt.Errorf("%s is already a recipient", name)
	}
	switch rec.Kind {
	case KindUser, KindBot:
	case KindMachine:
		if o, ok := r.Recipients[rec.Owner]; !ok || o.Kind != KindUser {
			return Registry{}, fmt.Errorf("the owner of a machine must be a user among the recipients")
		}
	default:
		return Registry{}, fmt.Errorf("kind must be user, machine or bot")
	}
	if rec.Key == "" {
		return Registry{}, errors.New("the key is empty")
	}
	if err := r.keyFree(rec.Key, ""); err != nil {
		return Registry{}, err
	}
	next := r.clone()
	if next.Recipients == nil {
		next.Recipients = map[string]Recipient{}
	}
	next.Recipients[name] = rec
	return next, nil
}

// Without is a copy of the registry without the entry.
func (r Registry) Without(name string) (Registry, error) {
	rec, ok := r.Recipients[name]
	if !ok {
		return Registry{}, fmt.Errorf("%s is not a recipient", name)
	}
	if len(r.Recipients) == 1 {
		return Registry{}, ErrLast
	}
	if rec.Kind == KindUser {
		var owned []string
		for _, n := range r.names() {
			if m := r.Recipients[n]; m.Kind == KindMachine && m.Owner == name {
				owned = append(owned, n)
			}
		}
		if len(owned) > 0 {
			return Registry{}, &OwnsError{Machines: owned}
		}
	}
	next := r.clone()
	delete(next.Recipients, name)
	return next, nil
}

// WithKey is a copy of the registry with the entry's key replaced.
func (r Registry) WithKey(name, key string) (Registry, error) {
	rec, ok := r.Recipients[name]
	if !ok {
		return Registry{}, fmt.Errorf("%s is not a recipient", name)
	}
	if key == "" {
		return Registry{}, errors.New("the key is empty")
	}
	if err := r.keyFree(key, name); err != nil {
		return Registry{}, err
	}
	rec.Key = key
	next := r.clone()
	next.Recipients[name] = rec
	return next, nil
}
