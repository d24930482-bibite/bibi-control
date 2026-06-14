package thebibites

import (
	"path"
	"regexp"
	"strings"
)

var (
	bibiteEntryRE = regexp.MustCompile(`^bibites/bibite_[0-9]+\.bb8$`)
	eggEntryRE    = regexp.MustCompile(`^eggs/egg_[0-9]+\.bb8$`)
)

func ClassifyEntry(name string) EntryKind {
	if strings.HasSuffix(name, "/") {
		return EntryDirectory
	}
	switch name {
	case "settings.bb8settings":
		return EntrySettings
	case "speciesData.json":
		return EntrySpecies
	case "scene.bb8scene":
		return EntryScene
	case "vars.bb8scene":
		return EntryVars
	case "pellets.bb8scene":
		return EntryPellets
	case "pheromones.bb8scene":
		return EntryPheromones
	case "data.bin":
		return EntryDataBin
	case "img.png":
		return EntryPreviewImage
	}
	if bibiteEntryRE.MatchString(name) {
		return EntryBibite
	}
	if eggEntryRE.MatchString(name) {
		return EntryEgg
	}
	if isJSONLikeName(name) {
		return EntryUnknownJSON
	}
	return EntryUnknownBinary
}

func isJSONKind(kind EntryKind) bool {
	switch kind {
	case EntrySettings, EntrySpecies, EntryScene, EntryVars, EntryPellets, EntryPheromones, EntryBibite, EntryEgg, EntryUnknownJSON:
		return true
	default:
		return false
	}
}

func isJSONLikeName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".bb8", ".bb8scene", ".bb8settings", ".json":
		return true
	default:
		return false
	}
}

func validateEntryName(name string) error {
	if name == "" {
		return parseError("empty zip entry name")
	}
	if strings.ContainsRune(name, '\x00') {
		return parseError("zip entry name contains NUL")
	}
	if strings.Contains(name, "\\") {
		return parseError("zip entry name uses backslashes")
	}
	if strings.HasPrefix(name, "/") {
		return parseError("zip entry name is absolute")
	}
	if len(name) >= 2 && name[1] == ':' {
		return parseError("zip entry name has a drive prefix")
	}

	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return parseError("zip entry name escapes archive root")
	}
	if strings.Contains(cleaned, "/../") {
		return parseError("zip entry name contains parent traversal")
	}
	withoutTrailingSlash := strings.TrimSuffix(name, "/")
	if cleaned != withoutTrailingSlash {
		return parseError("zip entry name is not normalized")
	}
	return nil
}

type parseError string

func (e parseError) Error() string {
	return string(e)
}
