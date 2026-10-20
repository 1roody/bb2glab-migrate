package git

import (
	"sort"
	"strings"
)

// Refs: ref (refs/heads/..., refs/tags/...) -> SHA.
type Refs map[string]string

// Divergence: ref presente na origem e no destino com SHAs diferentes.
type Divergence struct {
	Ref, SourceSHA, DestSHA string
}

// DiffRefs: refs da origem ausentes no destino e refs com SHA diferente. Refs só do destino não contam.
func DiffRefs(src, dst Refs) (missing []string, divergent []Divergence) {
	for _, ref := range SortedNames(src) {
		d, ok := dst[ref]
		switch {
		case !ok:
			missing = append(missing, ref)
		case d != src[ref]:
			divergent = append(divergent, Divergence{ref, src[ref], d})
		}
	}
	return missing, divergent
}

func SortedNames(m Refs) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func RefLabel(ref string) string {
	if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return "branch " + name
	}
	if name, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
		return "tag " + name
	}
	return ref
}

func ShortSHA(sha string) string { return sha[:min(8, len(sha))] }
