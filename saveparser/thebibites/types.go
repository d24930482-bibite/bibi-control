package thebibites

import "time"

type EntryKind string

const (
	EntrySettings      EntryKind = "settings"
	EntrySpecies       EntryKind = "species"
	EntryScene         EntryKind = "scene"
	EntryVars          EntryKind = "vars"
	EntryPellets       EntryKind = "pellets"
	EntryPheromones    EntryKind = "pheromones"
	EntryDataBin       EntryKind = "data_bin"
	EntryPreviewImage  EntryKind = "preview_image"
	EntryBibite        EntryKind = "bibite"
	EntryEgg           EntryKind = "egg"
	EntryDirectory     EntryKind = "directory"
	EntryUnknownJSON   EntryKind = "unknown_json"
	EntryUnknownBinary EntryKind = "unknown_binary"
)

type DiagnosticSeverity string

const (
	SeverityInfo    DiagnosticSeverity = "info"
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityError   DiagnosticSeverity = "error"
)

type Options struct {
	MaxArchiveBytes      int64
	MaxEntries           int
	MaxEntryBytes        uint64
	MaxUncompressedBytes uint64
}

func DefaultOptions() Options {
	return Options{
		MaxArchiveBytes:      512 * 1024 * 1024,
		MaxEntries:           10000,
		MaxEntryBytes:        128 * 1024 * 1024,
		MaxUncompressedBytes: 1024 * 1024 * 1024,
	}
}

type Archive struct {
	SourcePath string
	FileName   string
	Size       int64
	SHA256     string

	Entries []Entry

	Scene      *SceneState
	Vars       *GenericJSONState
	Settings   *SettingsState
	Species    *SpeciesData
	Bibites    []Bibite
	Eggs       []Egg
	PelletData *PelletData
	Pheromones []Pheromone

	Scalars     []Scalar
	Counts      DerivedCounts
	Diagnostics []Diagnostic
}

type ParsedEntry struct {
	Entry Entry

	Scene      *SceneState
	Vars       *GenericJSONState
	Generic    *GenericJSONState
	Settings   *SettingsState
	Species    *SpeciesData
	Bibite     *Bibite
	Egg        *Egg
	PelletData *PelletData
	Pheromones []Pheromone

	Scalars     []Scalar
	Diagnostics []Diagnostic
}

func (a *Archive) Entry(name string) *Entry {
	for i := range a.Entries {
		if a.Entries[i].Name == name {
			return &a.Entries[i]
		}
	}
	return nil
}

func (a *Archive) EntriesByKind(kind EntryKind) []Entry {
	entries := make([]Entry, 0)
	for _, entry := range a.Entries {
		if entry.Kind == kind {
			entries = append(entries, entry)
		}
	}
	return entries
}

type Entry struct {
	Index            int
	Name             string
	Kind             EntryKind
	Method           uint16
	CRC32            uint32
	CompressedSize   uint64
	UncompressedSize uint64
	Modified         time.Time
	SHA256           string
	HasUTF8BOM       bool
	Raw              []byte
	JSON             any
}

type Diagnostic struct {
	Severity DiagnosticSeverity
	Code     string
	Entry    string
	Message  string
}

type DerivedCounts struct {
	ArchiveEntryCount int
	BibiteFileCount   int
	EggFileCount      int
	JSONEntryCount    int
	UnknownEntryCount int

	ParsedBibites       int
	ParsedEggs          int
	UniqueBibiteBodyIDs int
	AliveBibites        int
	DeadBibites         int
	DyingBibites        int

	PelletGroups   int
	Pellets        int
	Pheromones     int
	SpeciesRecords int

	SceneReportedBibites int64
	HasSceneNBibites     bool
	SceneReportedPellets int64
	HasSceneNPellets     bool
}

type ScalarType string

const (
	ScalarNull   ScalarType = "null"
	ScalarBool   ScalarType = "bool"
	ScalarNumber ScalarType = "number"
	ScalarString ScalarType = "string"
)

type Scalar struct {
	EntryName string
	OwnerKind string
	OwnerID   string
	Path      string
	Type      ScalarType

	StringValue string
	NumberValue float64
	BoolValue   bool
	RawJSON     string
}

type GenericJSONState struct {
	EntryName string
	Raw       map[string]any
	Scalars   []Scalar
}

type SceneState struct {
	EntryName string
	Raw       map[string]any
	Scalars   []Scalar

	Version       string
	NBibites      int64
	HasNBibites   bool
	NPellets      int64
	HasNPellets   bool
	SimulatedTime float64
	HasTime       bool
}

type SettingsState struct {
	EntryName        string
	Raw              map[string]any
	Scalars          []Scalar
	Materials        []SettingsMaterial
	Zones            []SettingsZone
	BibiteSpawners   []SettingsBibiteSpawner
	SettingsChangers []SettingsChanger
}

type SettingsMaterial struct {
	Name    string
	Raw     map[string]any
	Scalars []Scalar
}

type SettingsZone struct {
	Index    int
	Name     string
	ID       int64
	HasID    bool
	Material string
	Raw      map[string]any
	Scalars  []Scalar
}

type SettingsBibiteSpawner struct {
	Index   int
	Path    string
	Raw     map[string]any
	Scalars []Scalar
}

type SettingsChanger struct {
	Index   int
	Name    string
	Raw     map[string]any
	Scalars []Scalar
}

type SpeciesData struct {
	EntryName        string
	Raw              map[string]any
	Scalars          []Scalar
	Records          []SpeciesRecord
	ActiveSpeciesIDs []int64
}

type SpeciesRecord struct {
	Index                     int
	SpeciesID                 int64
	HasSpeciesID              bool
	GenerationOfFirstSpecimen int64
	Favorite                  bool
	GenericName               string
	SpecificName              string
	Description               string
	TemplateVersion           string
	Raw                       map[string]any
	Scalars                   []Scalar
	TemplateGeneScalars       []Scalar
	TemplateBrainNodes        []BrainNode
	TemplateBrainSynapses     []BrainSynapse
}

type Transform struct {
	PositionX float64
	PositionY float64
	Rotation  float64
	Scale     float64
}

type RigidBody struct {
	PX float64
	PY float64
	VX float64
	VY float64
	R  float64
}

type Bibite struct {
	EntryName string
	FileIndex int
	Raw       map[string]any
	Scalars   []Scalar

	ID    int64
	HasID bool

	SpeciesID  int64
	Generation int64
	Dead       bool
	Dying      bool
	Health     float64
	Energy     float64
	TimeAlive  float64

	Transform Transform
	RigidBody RigidBody

	GeneScalars     []Scalar
	BodyScalars     []Scalar
	ClockScalars    []Scalar
	StomachContents []StomachContent
	Children        []ChildLink
	BrainNodes      []BrainNode
	BrainSynapses   []BrainSynapse
}

type Egg struct {
	EntryName string
	FileIndex int
	Raw       map[string]any
	Scalars   []Scalar

	ID    int64
	HasID bool

	SpeciesID     int64
	Generation    int64
	HatchProgress float64
	Energy        float64

	Transform Transform
	RigidBody RigidBody

	GeneScalars   []Scalar
	EggScalars    []Scalar
	BrainNodes    []BrainNode
	BrainSynapses []BrainSynapse
}

type StomachContent struct {
	Index              int
	EntryName          string
	BibiteID           int64
	HasBibiteID        bool
	Material           string
	Amount             float64
	AverageChunkAmount float64
	Raw                map[string]any
	Scalars            []Scalar
}

type ChildLink struct {
	Index       int
	EntryName   string
	ParentID    int64
	HasParentID bool
	ChildID     int64
}

type BrainNode struct {
	Index      int
	EntryName  string
	OwnerKind  string
	OwnerID    string
	Type       int64
	TypeName   string
	NodeIndex  int64
	Innovation int64
	Desc       string
	Archetype  int64

	BaseActivation float64
	Value          float64
	LastInput      float64
	LastOutput     float64

	Raw     map[string]any
	Scalars []Scalar
}

type BrainSynapse struct {
	Index      int
	EntryName  string
	OwnerKind  string
	OwnerID    string
	Innovation int64
	NodeIn     int64
	NodeOut    int64
	Weight     float64
	Enabled    bool

	Raw     map[string]any
	Scalars []Scalar
}

type PelletData struct {
	EntryName string
	Raw       map[string]any
	Scalars   []Scalar
	Groups    []PelletGroup
	Pellets   []Pellet
}

type PelletGroup struct {
	Index   int
	Zone    string
	Count   int
	Raw     map[string]any
	Scalars []Scalar
}

type Pellet struct {
	Index      int
	GroupIndex int
	EntryName  string
	Zone       string
	Raw        map[string]any
	Scalars    []Scalar

	Transform Transform
	RigidBody RigidBody
	Material  string
	Amount    float64
}

type Pheromone struct {
	Index     int
	EntryName string
	Raw       map[string]any
	Scalars   []Scalar
}
