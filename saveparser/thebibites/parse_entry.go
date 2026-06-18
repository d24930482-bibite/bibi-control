package thebibites

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func ParseEntryBytes(name string, raw []byte) (*ParsedEntry, error) {
	return ParseEntryBytesAs(name, ClassifyEntry(name), raw)
}

func ParseBytesAs(kind EntryKind, raw []byte) (*ParsedEntry, error) {
	name, err := defaultEntryName(kind)
	if err != nil {
		return nil, err
	}
	return ParseEntryBytesAs(name, kind, raw)
}

func ParseEntryBytesAs(name string, kind EntryKind, raw []byte) (*ParsedEntry, error) {
	if err := validateEntryName(name); err != nil {
		return nil, fmt.Errorf("unsafe entry name %q: %w", name, err)
	}
	if kind == EntryDirectory {
		return nil, fmt.Errorf("cannot parse directory entry %q as payload", name)
	}

	rawCopy := append([]byte(nil), raw...)
	sum := sha256.Sum256(rawCopy)
	entry := Entry{
		Index:            0,
		Name:             name,
		Kind:             kind,
		UncompressedSize: uint64(len(rawCopy)),
		SHA256:           hex.EncodeToString(sum[:]),
		Raw:              rawCopy,
	}

	result := parseEntryPayload(&entry)

	return &ParsedEntry{
		Entry:       entry,
		Scene:       result.Scene,
		Vars:        result.Vars,
		Generic:     result.Generic,
		Settings:    result.Settings,
		Species:     result.Species,
		Bibite:      result.Bibite,
		Egg:         result.Egg,
		PelletData:  result.PelletData,
		Pheromones:  result.Pheromones,
		Diagnostics: result.Diagnostics,
	}, nil
}

func defaultEntryName(kind EntryKind) (string, error) {
	switch kind {
	case EntrySettings:
		return "settings.bb8settings", nil
	case EntrySpecies:
		return "speciesData.json", nil
	case EntryScene:
		return "scene.bb8scene", nil
	case EntryVars:
		return "vars.bb8scene", nil
	case EntryPellets:
		return "pellets.bb8scene", nil
	case EntryPheromones:
		return "pheromones.bb8scene", nil
	case EntryDataBin:
		return "data.bin", nil
	case EntryPreviewImage:
		return "img.png", nil
	case EntryBibite:
		return "bibites/bibite_0.bb8", nil
	case EntryEgg:
		return "eggs/egg_0.bb8", nil
	case EntryUnknownJSON:
		return "unknown.json", nil
	case EntryUnknownBinary:
		return "unknown.bin", nil
	default:
		return "", fmt.Errorf("no default entry name for kind %q", kind)
	}
}
