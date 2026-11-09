package upgrade

import "strings"

// slashAll : chemins en « / » pour comparer de la même façon sous Windows et Linux.
func slashAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ReplaceAll(s, "\\", "/")
	}
	return out
}
