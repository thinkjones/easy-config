// Package backend is the encryption backend ezc stores secrets with. Part 1 holds only Pair.
package backend

// Pair is one name and its value, in the order its file declares it.
type Pair struct{ Name, Value string }
