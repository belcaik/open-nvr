package nvrwatch

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
)

const (
	Added   = "añadido"
	Removed = "eliminado"
	Changed = "cambiado"
)

type Change struct {
	Kind       string
	Key        string
	Old        string
	New        string
	oldPresent bool
	newPresent bool
}

func section(key string) string {
	if before, _, ok := strings.Cut(key, "."); ok {
		return before
	}
	return key
}

func contains(items []string, wanted string) bool {
	return slices.Contains(items, wanted)
}

func ignored(key string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if key == prefix || strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func diffBaseline(base, current map[string]string, sections, ignorePrefixes []string) []Change {
	keys := map[string]bool{}
	for key := range base {
		keys[key] = true
	}
	for key := range current {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		if contains(sections, section(key)) && !ignored(key, ignorePrefixes) {
			ordered = append(ordered, key)
		}
	}
	slices.Sort(ordered)
	changes := make([]Change, 0)
	for _, key := range ordered {
		old, oldOK := base[key]
		value, currentOK := current[key]
		switch {
		case !oldOK:
			changes = append(changes, Change{Kind: Added, Key: key, New: value, newPresent: true})
		case !currentOK:
			changes = append(changes, Change{Kind: Removed, Key: key, Old: old, oldPresent: true})
		case old != value:
			changes = append(changes, Change{Kind: Changed, Key: key, Old: old, New: value, oldPresent: true, newPresent: true})
		}
	}
	return changes
}

func fingerprint(changes []Change) string {
	items := append([]Change(nil), changes...)
	slices.SortFunc(items, func(a, b Change) int {
		return strings.Compare(changeRepr(a), changeRepr(b))
	})
	h := sha256.New()
	for _, change := range items {
		fmt.Fprint(h, changeRepr(change))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func pythonRepr(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return "'" + replacer.Replace(value) + "'"
}

func optionalRepr(value string, present bool) string {
	if !present {
		return "None"
	}
	return pythonRepr(value)
}

func changeRepr(change Change) string {
	return fmt.Sprintf("(%s, %s, %s, %s)", pythonRepr(change.Kind), pythonRepr(change.Key), optionalRepr(change.Old, change.oldPresent), optionalRepr(change.New, change.newPresent))
}

func formatChanges(changes []Change, limit int) string {
	if limit <= 0 {
		limit = 25
	}
	lines := make([]string, 0, min(len(changes), limit)+1)
	for _, change := range changes[:min(len(changes), limit)] {
		switch change.Kind {
		case Added:
			lines = append(lines, fmt.Sprintf("+ %s = %s", change.Key, pythonRepr(change.New)))
		case Removed:
			lines = append(lines, fmt.Sprintf("- %s (era %s)", change.Key, pythonRepr(change.Old)))
		default:
			lines = append(lines, fmt.Sprintf("~ %s: %s -> %s", change.Key, pythonRepr(change.Old), pythonRepr(change.New)))
		}
	}
	if len(changes) > limit {
		lines = append(lines, fmt.Sprintf("... y %d cambios más", len(changes)-limit))
	}
	return strings.Join(lines, "\n")
}

func mergeBaseline(base, current map[string]string, sections []string) map[string]string {
	out := map[string]string{}
	for key, value := range base {
		if !contains(sections, section(key)) {
			out[key] = value
		}
	}
	for key, value := range current {
		if contains(sections, section(key)) {
			out[key] = value
		}
	}
	return out
}
