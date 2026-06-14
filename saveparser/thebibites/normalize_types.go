package thebibites

type ExtractedSave struct {
	Archive     SaveArchiveRow
	Entries     []SaveEntryRow
	Diagnostics []DiagnosticRow
	Scene       *SceneRow
	Vars        *VarsRow

	SceneColorSelectors []SceneColorSelectorRow
	ScenePheroTowers    []SceneTowerRow
	SceneRadTowers      []SceneTowerRow

	SettingsSimulationValues  []SettingValueRow
	SettingsIndependentValues []SettingValueRow
	SettingsMaterials         []SettingsMaterialRow
	SettingsMaterialValues    []SettingValueRow
	SettingsZones             []SettingsZoneRow
	SettingsZoneGeometry      []SettingsZoneGeometryRow
	SettingsZoneValues        []SettingValueRow
	SettingsZoneGroups        []SettingsZoneGroupRow
	SettingsBibiteSpawners    []SettingsBibiteSpawnerRow
	SettingsChangers          []SettingsChangerRow
	SettingsChangerPoints     []SettingsChangerPointRow
	SettingsChangerTargets    []SettingsChangerTargetRow

	ActiveSpecies        []ActiveSpeciesRow
	Species              []SpeciesRow
	SpeciesGenes         []GeneRow
	SpeciesBrainNodes    []BrainNodeRow
	SpeciesBrainSynapses []BrainSynapseRow

	Bibites                 []BibiteRow
	BibiteGenes             []GeneRow
	BibiteBody              []BibiteBodyRow
	BibiteMouth             []BibiteMouthRow
	BibitePheromoneEmitters []BibitePheromoneEmitterRow
	BibiteEggLayers         []BibiteEggLayerRow
	BibiteControl           []BibiteControlRow
	BibiteStomachContents   []StomachContentRow
	BibiteChildren          []BibiteChildRow
	BibiteBrainNodes        []BrainNodeRow
	BibiteBrainSynapses     []BrainSynapseRow

	Eggs             []EggRow
	EggGenes         []GeneRow
	EggBrainNodes    []BrainNodeRow
	EggBrainSynapses []BrainSynapseRow

	PelletGroups []PelletGroupRow
	Pellets      []PelletRow
	Pheromones   []PheromoneRow

	JSONScalars []ScalarRow
}

type SaveArchiveRow struct {
	SaveID     string
	SourcePath string
	FileName   string
	SizeBytes  int64
	SHA256     string
}

type SaveEntryRow struct {
	SaveID           string
	EntryIndex       int
	EntryName        string
	Kind             EntryKind
	SHA256           string
	CompressedSize   uint64
	UncompressedSize uint64
	HasUTF8BOM       bool
}

type DiagnosticRow struct {
	SaveID    string
	EntryName string
	Severity  DiagnosticSeverity
	Code      string
	Message   string
}

type SceneRow struct {
	SaveID              string
	EntryName           string
	Version             string
	SimulatedTime       float64
	HasSimulatedTime    bool
	ReportedNBibites    int64
	HasReportedNBibites bool
	ReportedNPellets    int64
	HasReportedNPellets bool
	ParsedBibites       int
	ParsedEggs          int
	AliveBibites        int
	DeadBibites         int
	DyingBibites        int
	ParsedPellets       int
}

type VarsRow struct {
	SaveID        string
	EntryName     string
	TowerMaxID    int64
	HasTowerMaxID bool
}

type SceneColorSelectorRow struct {
	SaveID             string
	EntryName          string
	ColorSelectorIndex int
	RawJSON            string
}

type SceneTowerRow struct {
	SaveID     string
	EntryName  string
	TowerKind  string
	TowerIndex int
	RawJSON    string
}

type SettingValueRow struct {
	SaveID         string
	EntryName      string
	Scope          string
	OwnerKind      string
	OwnerID        string
	SettingName    string
	Path           string
	Type           ScalarType
	NumberValue    float64
	StringValue    string
	BoolValue      bool
	RawJSON        string
	WrapperRawJSON string
}

type SettingsMaterialRow struct {
	SaveID        string
	EntryName     string
	MaterialIndex int
	MaterialName  string
	RawJSON       string
}

type SettingsZoneRow struct {
	SaveID       string
	EntryName    string
	ZoneIndex    int
	ZoneID       int64
	HasZoneID    bool
	Name         string
	Material     string
	Distribution string
	RawJSON      string
}

type SettingsZoneGeometryRow struct {
	SaveID           string
	EntryName        string
	ZoneIndex        int
	ZoneID           int64
	HasZoneID        bool
	GeometryIndex    int
	GeometryKind     string
	PositionX        float64
	PositionY        float64
	Radius           float64
	RadiusIsRelative bool
	RawJSON          string
}

type SettingsZoneGroupRow struct {
	SaveID     string
	EntryName  string
	GroupIndex int
	Name       string
	RawJSON    string
}

type SettingsBibiteSpawnerRow struct {
	SaveID         string
	EntryName      string
	SpawnerIndex   int
	Path           string
	SpawnPriority  float64
	Minimum        float64
	RandomizeGenes string
	GrowthAtSpawn  string
	Tagging        string
	SpawnType      string
	TotalSpawned   int64
	RawJSON        string
}

type SettingsChangerRow struct {
	SaveID       string
	EntryName    string
	ChangerIndex int
	Name         string
	Repeat       bool
	Start        float64
	RawJSON      string
}

type SettingsChangerPointRow struct {
	SaveID       string
	EntryName    string
	ChangerIndex int
	PointIndex   int
	T            float64
	Y            float64
	D            string
	F            float64
}

type SettingsChangerTargetRow struct {
	SaveID       string
	EntryName    string
	ChangerIndex int
	TargetKey    string
	Scope        string
	ZoneIndex    int
	ZoneID       int64
	HasZoneID    bool
	SettingName  string
	Type         ScalarType
	NumberValue  float64
	StringValue  string
	BoolValue    bool
	RawJSON      string
}

type ActiveSpeciesRow struct {
	SaveID             string
	EntryName          string
	ActiveSpeciesIndex int
	SpeciesID          int64
}

type SpeciesRow struct {
	SaveID                    string
	EntryName                 string
	SpeciesIndex              int
	SpeciesID                 int64
	HasSpeciesID              bool
	ParentID                  int64
	HasParentID               bool
	GenerationOfFirstSpecimen int64
	TimeCreation              float64
	Favorite                  bool
	GenericName               string
	SpecificName              string
	Description               string
	TemplateVersion           string
}

type GeneRow struct {
	SaveID      string
	OwnerKind   string
	OwnerID     string
	EntryName   string
	GeneName    string
	Path        string
	Type        ScalarType
	NumberValue float64
	BoolValue   bool
	StringValue string
	RawJSON     string
}

type BrainNodeRow struct {
	SaveID         string
	OwnerKind      string
	OwnerID        string
	EntryName      string
	NodeRowIndex   int
	NodeIndex      int64
	Innovation     int64
	Type           int64
	TypeName       string
	Desc           string
	Archetype      int64
	BaseActivation float64
	Value          float64
	LastInput      float64
	LastOutput     float64
}

type BrainSynapseRow struct {
	SaveID          string
	OwnerKind       string
	OwnerID         string
	EntryName       string
	SynapseRowIndex int
	Innovation      int64
	NodeIn          int64
	NodeOut         int64
	Weight          float64
	Enabled         bool
}

type BibiteRow struct {
	SaveID             string
	EntryName          string
	BodyID             int64
	HasBodyID          bool
	SpeciesID          int64
	Generation         int64
	Dead               bool
	Dying              bool
	Health             float64
	Energy             float64
	TimeAlive          float64
	TransformPositionX float64
	TransformPositionY float64
	TransformRotation  float64
	TransformScale     float64
	RB2DPX             float64
	RB2DPY             float64
	RB2DVX             float64
	RB2DVY             float64
	RB2DR              float64
}

type BibiteBodyRow struct {
	SaveID              string
	EntryName           string
	BodyID              int64
	HasBodyID           bool
	D2Size              float64
	FatReservesAmount   float64
	AttackedDmg         float64
	TimesAttacked       float64
	TotalDamageSuffered float64
	BrainTicksCount     float64
	VisionLookupCount   float64
	VisionSensingCount  float64
	CorpseEnergyOffset  float64
}

type BibiteMouthRow struct {
	SaveID            string
	EntryName         string
	BodyID            int64
	HasBodyID         bool
	AttackedLastFrame bool
	BibitesBitten     float64
	BiteProgress      float64
	MurderedArea      float64
	TotalDamageDealt  float64
	TotalMurders      float64
}

type BibitePheromoneEmitterRow struct {
	SaveID    string
	EntryName string
	BodyID    int64
	HasBodyID bool
	Progress  float64
}

type BibiteEggLayerRow struct {
	SaveID      string
	EntryName   string
	BodyID      int64
	HasBodyID   bool
	EggProgress float64
	NEggsLaid   float64
}

type BibiteControlRow struct {
	SaveID      string
	EntryName   string
	BodyID      int64
	HasBodyID   bool
	TotalTravel float64
}

type StomachContentRow struct {
	SaveID             string
	EntryName          string
	BodyID             int64
	HasBodyID          bool
	ContentIndex       int
	Material           string
	Amount             float64
	AverageChunkAmount float64
}

type BibiteChildRow struct {
	SaveID       string
	EntryName    string
	ParentBodyID int64
	HasParentID  bool
	ChildIndex   int
	ChildBodyID  int64
}

type EggRow struct {
	SaveID             string
	EntryName          string
	EggID              int64
	HasEggID           bool
	SpeciesID          int64
	Generation         int64
	HatchProgress      float64
	Energy             float64
	TransformPositionX float64
	TransformPositionY float64
	TransformRotation  float64
	TransformScale     float64
	RB2DPX             float64
	RB2DPY             float64
	RB2DVX             float64
	RB2DVY             float64
	RB2DR              float64
}

type PelletGroupRow struct {
	SaveID      string
	EntryName   string
	GroupIndex  int
	Zone        string
	PelletCount int
}

type PelletRow struct {
	SaveID               string
	EntryName            string
	PelletIndex          int
	GroupIndex           int
	GroupPelletIndex     int
	Zone                 string
	Material             string
	Amount               float64
	MatterDecayTimeAlive float64
	MatterDecayRotAmount float64
	HasMatterDecay       bool
	TransformPositionX   float64
	TransformPositionY   float64
	TransformRotation    float64
	TransformScale       float64
	RB2DPX               float64
	RB2DPY               float64
	RB2DVX               float64
	RB2DVY               float64
	RB2DR                float64
}

type PheromoneRow struct {
	SaveID             string
	EntryName          string
	PheromoneIndex     int
	TransformPositionX float64
	TransformPositionY float64
	TransformRotation  float64
	TransformScale     float64
	HeadingRawJSON     string
	RStrength          float64
	GStrength          float64
	BStrength          float64
	NR                 float64
	NG                 float64
	NB                 float64
}

type ScalarRow struct {
	SaveID      string
	EntryName   string
	OwnerKind   string
	OwnerID     string
	Path        string
	Type        ScalarType
	NumberValue float64
	StringValue string
	BoolValue   bool
	RawJSON     string
}
