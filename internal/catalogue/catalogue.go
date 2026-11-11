// Package catalogue turns the name of a credential into the name of the system
// it is for, such as JIRA_API_TOKEN into "Jira". It ships with the agent and is
// the same for everyone: there is no list to keep per machine or per
// organization. A name it does not recognise is not turned into anything, and
// by default is never sent.
package catalogue

import (
	_ "embed"
	"sort"
	"strings"
)

//go:embed systems.txt
var source string

type entry struct {
	system   string
	prefixes []string
}

var entries = parse(source)

// parse reads the catalogue file: "System = PREFIX_ OTHER_" on each line.
func parse(text string) []entry {
	var found []entry
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, prefixes, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		e := entry{system: strings.TrimSpace(name)}
		for _, prefix := range strings.Fields(prefixes) {
			e.prefixes = append(e.prefixes, strings.ToUpper(prefix))
		}
		if e.system != "" && len(e.prefixes) > 0 {
			found = append(found, e)
		}
	}
	return found
}

// System names the system a credential is for. The second result is false
// when the catalogue does not recognise the name.
func System(credential string) (string, bool) {
	name := strings.ToUpper(strings.TrimSpace(credential))
	for _, e := range entries {
		for _, prefix := range e.prefixes {
			if strings.HasPrefix(name, prefix) {
				return e.system, true
			}
		}
	}
	return "", false
}

// Sort turns credential names into the systems they are for, each named once
// and in order, and returns apart the names the catalogue does not recognise.
func Sort(credentials []string) (systems []string, unrecognised []string) {
	seenSystem := map[string]bool{}
	seenName := map[string]bool{}
	for _, credential := range credentials {
		if system, ok := System(credential); ok {
			if !seenSystem[system] {
				seenSystem[system] = true
				systems = append(systems, system)
			}
			continue
		}
		if name := strings.TrimSpace(credential); name != "" && !seenName[name] {
			seenName[name] = true
			unrecognised = append(unrecognised, name)
		}
	}
	sort.Strings(systems)
	sort.Strings(unrecognised)
	return systems, unrecognised
}
