package thebibites

import "fmt"

func (a *Archive) recomputeCounts() {
	var counts DerivedCounts
	counts.ArchiveEntryCount = len(a.Entries)
	for _, entry := range a.Entries {
		switch entry.Kind {
		case EntryBibite:
			counts.BibiteFileCount++
		case EntryEgg:
			counts.EggFileCount++
		case EntryUnknownJSON, EntryUnknownBinary:
			counts.UnknownEntryCount++
		}
		if isJSONKind(entry.Kind) {
			counts.JSONEntryCount++
		}
	}
	counts.ParsedBibites = len(a.Bibites)
	counts.ParsedEggs = len(a.Eggs)
	counts.Pheromones = len(a.Pheromones)
	if a.PelletData != nil {
		counts.PelletGroups = len(a.PelletData.Groups)
		counts.Pellets = len(a.PelletData.Pellets)
	}
	if a.Species != nil {
		counts.SpeciesRecords = len(a.Species.Records)
	}
	if a.Scene != nil {
		counts.SceneReportedBibites = a.Scene.NBibites
		counts.HasSceneNBibites = a.Scene.HasNBibites
		counts.SceneReportedPellets = a.Scene.NPellets
		counts.HasSceneNPellets = a.Scene.HasNPellets
	}

	countBibiteStates(&counts, a.Bibites)
	a.Counts = counts
	a.addCountDiagnostics(counts)
}

func countBibiteStates(counts *DerivedCounts, bibites []Bibite) {
	seenIDs := make(map[int64]struct{}, len(bibites))
	for _, bibite := range bibites {
		if bibite.HasID {
			seenIDs[bibite.ID] = struct{}{}
		}
		if bibite.Dead {
			counts.DeadBibites++
		}
		if bibite.Dying {
			counts.DyingBibites++
		}
		if !bibite.Dead && !bibite.Dying {
			counts.AliveBibites++
		}
	}
	counts.UniqueBibiteBodyIDs = len(seenIDs)
}

func (a *Archive) addCountDiagnostics(counts DerivedCounts) {
	if counts.HasSceneNBibites && counts.SceneReportedBibites != int64(counts.ParsedBibites) {
		a.addDiagnostic(
			SeverityWarning,
			"scene_nbibites_mismatch",
			"scene.bb8scene",
			fmt.Sprintf("scene reports nBibites=%d, parsed bibite files=%d", counts.SceneReportedBibites, counts.ParsedBibites),
		)
	}
	if counts.HasSceneNPellets && counts.SceneReportedPellets != int64(counts.Pellets) {
		a.addDiagnostic(
			SeverityWarning,
			"scene_npellets_mismatch",
			"scene.bb8scene",
			fmt.Sprintf("scene reports nPellets=%d, parsed pellets=%d", counts.SceneReportedPellets, counts.Pellets),
		)
	}
}

func (a *Archive) addDiagnostic(severity DiagnosticSeverity, code, entry, message string) {
	a.Diagnostics = append(a.Diagnostics, Diagnostic{
		Severity: severity,
		Code:     code,
		Entry:    entry,
		Message:  message,
	})
}
