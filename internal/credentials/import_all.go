package credentials

import (
	"errors"
	"sort"
)

type BulkImportResult struct {
	Imported   int `json:"imported"`
	Kept       int `json:"kept"`
	Duplicates int `json:"duplicates"`
}

// ImportAll retains existing URL/username pairs and atomically imports all new
// pairs. A concurrent edit to an existing match aborts the whole transaction.
func (s *Store) ImportAll(entries []ImportEntry, source string) (BulkImportResult, error) {
	result := BulkImportResult{}
	if len(entries) == 0 || len(entries) > 10000 {
		return result, errors.New("import requires 1 to 10000 entries")
	}
	conflicts := s.ImportConflicts(entries)
	seen := map[string]bool{}
	choices := []ImportSelection{}
	for i, e := range entries {
		key := e.URL + "\x00" + e.Username
		if seen[key] {
			result.Duplicates++
			continue
		}
		seen[key] = true
		choice := ImportSelection{Index: i, Action: "new"}
		matches := conflicts[i]
		if len(matches) > 0 {
			sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
			choice.Action = "keep"
			choice.Reference = matches[0].ID
			choice.Version = matches[0].Version
			result.Kept++
		} else {
			result.Imported++
		}
		choices = append(choices, choice)
	}
	if _, err := s.importWithConflicts(entries, choices, 10000, source); err != nil {
		return BulkImportResult{}, err
	}
	return result, nil
}
