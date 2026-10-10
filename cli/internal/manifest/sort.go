package manifest

import "sort"

// SortedNames gives a stable iteration order over a manifest map. Generated
// output must be byte-identical across runs or the CI drift check is useless,
// and Go randomises map iteration deliberately.
func SortedNames(m map[string]Var) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
