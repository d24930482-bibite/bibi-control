package thebibites

//go:generate go run ../../cmd/gen_thebibites_schema

// SQLRefResolverKind declares how a normalized table can be resolved back to an
// archive mutation target. It is intentionally a small shape vocabulary; the
// mutator owns the semantics for each shape.
type SQLRefResolverKind string

const (
	SQLRefResolverBibitePathMap               SQLRefResolverKind = "bibite_path_map"
	SQLRefResolverEggPathMap                  SQLRefResolverKind = "egg_path_map"
	SQLRefResolverBibiteStomachContentPathMap SQLRefResolverKind = "bibite_stomach_content_path_map"
	SQLRefResolverBibiteBrainNodePathMap      SQLRefResolverKind = "bibite_brain_node_path_map"
	SQLRefResolverBibiteBrainSynapsePathMap   SQLRefResolverKind = "bibite_brain_synapse_path_map"
	SQLRefResolverEggBrainNodePathMap         SQLRefResolverKind = "egg_brain_node_path_map"
	SQLRefResolverEggBrainSynapsePathMap      SQLRefResolverKind = "egg_brain_synapse_path_map"
	SQLRefResolverPelletPathMap               SQLRefResolverKind = "pellet_path_map"
	SQLRefResolverPheromonePathMap            SQLRefResolverKind = "pheromone_path_map"
	SQLRefResolverSettingsZonePathMap         SQLRefResolverKind = "settings_zone_path_map"
	SQLRefResolverBibiteGeneValue             SQLRefResolverKind = "bibite_gene_value"
	SQLRefResolverEggGeneValue                SQLRefResolverKind = "egg_gene_value"
	SQLRefResolverSettingsValue               SQLRefResolverKind = "settings_value"
	SQLRefResolverSettingsChangerTarget       SQLRefResolverKind = "settings_changer_target"
)

type ExtractedSave struct {
	Archive     SaveArchiveRow  `dbtable:"save_archives"`
	Entries     []SaveEntryRow  `dbtable:"save_entries"`
	Diagnostics []DiagnosticRow `dbtable:"diagnostics"`
	Scene       *SceneRow       `dbtable:"scenes"`
	Vars        *VarsRow        `dbtable:"vars"`

	SceneColorSelectors []SceneColorSelectorRow `dbtable:"scene_color_selectors"`
	ScenePheroTowers    []SceneTowerRow         `dbtable:"scene_phero_towers"`
	SceneRadTowers      []SceneTowerRow         `dbtable:"scene_rad_towers"`

	SettingsSimulationValues  []SettingValueRow          `dbtable:"settings_simulation_values" sqlrefresolver:"settings_value"`
	SettingsIndependentValues []SettingValueRow          `dbtable:"settings_independent_values" sqlrefresolver:"settings_value"`
	SettingsMaterials         []SettingsMaterialRow      `dbtable:"settings_materials"`
	SettingsMaterialValues    []SettingValueRow          `dbtable:"settings_material_values" sqlrefresolver:"settings_value"`
	SettingsZones             []SettingsZoneRow          `dbtable:"settings_zones" sqlrefresolver:"settings_zone_path_map"`
	SettingsZoneGeometry      []SettingsZoneGeometryRow  `dbtable:"settings_zone_geometry"`
	SettingsZoneValues        []SettingValueRow          `dbtable:"settings_zone_values" sqlrefresolver:"settings_value"`
	SettingsZoneGroups        []SettingsZoneGroupRow     `dbtable:"settings_zone_groups"`
	SettingsBibiteSpawners    []SettingsBibiteSpawnerRow `dbtable:"settings_bibite_spawners"`
	SettingsChangers          []SettingsChangerRow       `dbtable:"settings_changers"`
	SettingsChangerPoints     []SettingsChangerPointRow  `dbtable:"settings_changer_points"`
	SettingsChangerTargets    []SettingsChangerTargetRow `dbtable:"settings_changer_targets" sqlrefresolver:"settings_changer_target"`

	ActiveSpecies        []ActiveSpeciesRow `dbtable:"active_species"`
	Species              []SpeciesRow       `dbtable:"species"`
	SpeciesGenes         []GeneRow          `dbtable:"species_genes"`
	SpeciesBrainNodes    []BrainNodeRow     `dbtable:"species_brain_nodes"`
	SpeciesBrainSynapses []BrainSynapseRow  `dbtable:"species_brain_synapses"`

	Bibites                 []BibiteRow                 `dbtable:"bibites" sqlrefresolver:"bibite_path_map"`
	BibiteGenes             []GeneRow                   `dbtable:"bibite_genes" sqlrefresolver:"bibite_gene_value"`
	BibiteBody              []BibiteBodyRow             `dbtable:"bibite_body" sqlrefresolver:"bibite_path_map"`
	BibiteMouth             []BibiteMouthRow            `dbtable:"bibite_mouth" sqlrefresolver:"bibite_path_map"`
	BibitePheromoneEmitters []BibitePheromoneEmitterRow `dbtable:"bibite_pheromone_emitters" sqlrefresolver:"bibite_path_map"`
	BibiteEggLayers         []BibiteEggLayerRow         `dbtable:"bibite_egg_layers" sqlrefresolver:"bibite_path_map"`
	BibiteControl           []BibiteControlRow          `dbtable:"bibite_control" sqlrefresolver:"bibite_path_map"`
	BibiteStomachContents   []StomachContentRow         `dbtable:"bibite_stomach_contents" sqlrefresolver:"bibite_stomach_content_path_map"`
	BibiteChildren          []BibiteChildRow            `dbtable:"bibite_children"`
	BibiteBrainNodes        []BrainNodeRow              `dbtable:"bibite_brain_nodes" sqlrefresolver:"bibite_brain_node_path_map"`
	BibiteBrainSynapses     []BrainSynapseRow           `dbtable:"bibite_brain_synapses" sqlrefresolver:"bibite_brain_synapse_path_map"`

	Eggs             []EggRow          `dbtable:"eggs" sqlrefresolver:"egg_path_map"`
	EggGenes         []GeneRow         `dbtable:"egg_genes" sqlrefresolver:"egg_gene_value"`
	EggBrainNodes    []BrainNodeRow    `dbtable:"egg_brain_nodes" sqlrefresolver:"egg_brain_node_path_map"`
	EggBrainSynapses []BrainSynapseRow `dbtable:"egg_brain_synapses" sqlrefresolver:"egg_brain_synapse_path_map"`

	PelletGroups []PelletGroupRow `dbtable:"pellet_groups"`
	Pellets      []PelletRow      `dbtable:"pellets" sqlrefresolver:"pellet_path_map"`
	Pheromones   []PheromoneRow   `dbtable:"pheromones" sqlrefresolver:"pheromone_path_map"`
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
	NumberValue    float64 `sqlrefvalue:"number"`
	StringValue    string  `sqlrefvalue:"string"`
	BoolValue      bool    `sqlrefvalue:"bool"`
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
	Name         string `sqlref:"name"`
	Material     string `sqlref:"material"`
	Distribution string `sqlref:"distribution"`
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
	NumberValue  float64 `sqlrefvalue:"number"`
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
	NumberValue float64 `sqlrefvalue:"number"`
	BoolValue   bool    `sqlrefvalue:"bool"`
	StringValue string  `sqlrefvalue:"string"`
	RawJSON     string
}

type BrainNodeRow struct {
	SaveID         string
	OwnerKind      string
	OwnerID        string
	EntryName      string
	NodeRowIndex   int
	NodeIndex      int64   `sqlref:"Index"`
	Innovation     int64   `sqlref:"Inov"`
	Type           int64   `sqlref:"Type"`
	TypeName       string  `sqlref:"TypeName"`
	Desc           string  `sqlref:"Desc"`
	Archetype      int64   `sqlref:"archetype"`
	BaseActivation float64 `sqlref:"baseActivation"`
	Value          float64 `sqlref:"Value"`
	LastInput      float64 `sqlref:"LastInput"`
	LastOutput     float64 `sqlref:"LastOutput"`
}

type BrainSynapseRow struct {
	SaveID          string
	OwnerKind       string
	OwnerID         string
	EntryName       string
	SynapseRowIndex int
	Innovation      int64   `sqlref:"Inov"`
	NodeIn          int64   `sqlref:"NodeIn"`
	NodeOut         int64   `sqlref:"NodeOut"`
	Weight          float64 `sqlref:"Weight"`
	Enabled         bool    `sqlref:"En"`
}

type BibiteRow struct {
	SaveID             string
	EntryName          string
	BodyID             int64
	HasBodyID          bool
	SpeciesID          int64   `sqlref:"genes.speciesID"`
	Generation         int64   `sqlref:"genes.gen"`
	Dead               bool    `sqlref:"body.dead"`
	Dying              bool    `sqlref:"body.dying"`
	Health             float64 `sqlref:"body.health"`
	Energy             float64 `sqlref:"body.energy"`
	TimeAlive          float64 `sqlref:"clock.timeAlive"`
	TransformPositionX float64 `sqlref:"transform.position[0]"`
	TransformPositionY float64 `sqlref:"transform.position[1]"`
	TransformRotation  float64 `sqlref:"transform.rotation"`
	TransformScale     float64 `sqlref:"transform.scale"`
	RB2DPX             float64 `sqlref:"rb2d.px"`
	RB2DPY             float64 `sqlref:"rb2d.py"`
	RB2DVX             float64 `sqlref:"rb2d.vx"`
	RB2DVY             float64 `sqlref:"rb2d.vy"`
	RB2DR              float64 `sqlref:"rb2d.r"`
}

type BibiteBodyRow struct {
	SaveID              string
	EntryName           string
	BodyID              int64
	HasBodyID           bool
	D2Size              float64 `sqlref:"body.d2Size"`
	FatReservesAmount   float64 `sqlref:"body.fatReservesAmount"`
	AttackedDmg         float64 `sqlref:"body.attackedDmg"`
	TimesAttacked       float64 `sqlref:"body.timesAttacked"`
	TotalDamageSuffered float64 `sqlref:"body.totalDamageSuffered"`
	BrainTicksCount     float64 `sqlref:"body.brainTicksCount"`
	VisionLookupCount   float64 `sqlref:"body.visionLookupCount"`
	VisionSensingCount  float64 `sqlref:"body.visionSensingCount"`
	CorpseEnergyOffset  float64 `sqlref:"body.corpseEnergyOffset"`
}

type BibiteMouthRow struct {
	SaveID            string
	EntryName         string
	BodyID            int64
	HasBodyID         bool
	AttackedLastFrame bool    `sqlref:"body.mouth.attackedLastFrame"`
	BibitesBitten     float64 `sqlref:"body.mouth.bibitesBitten"`
	BiteProgress      float64 `sqlref:"body.mouth.biteProgress"`
	MurderedArea      float64 `sqlref:"body.mouth.murderedArea"`
	TotalDamageDealt  float64 `sqlref:"body.mouth.totalDamageDealt"`
	TotalMurders      float64 `sqlref:"body.mouth.totalMurders"`
}

type BibitePheromoneEmitterRow struct {
	SaveID    string
	EntryName string
	BodyID    int64
	HasBodyID bool
	Progress  float64 `sqlref:"body.phero.progress"`
}

type BibiteEggLayerRow struct {
	SaveID      string
	EntryName   string
	BodyID      int64
	HasBodyID   bool
	EggProgress float64 `sqlref:"body.eggLayer.eggProgress"`
	NEggsLaid   float64 `sqlref:"body.eggLayer.nEggsLaid"`
}

type BibiteControlRow struct {
	SaveID      string
	EntryName   string
	BodyID      int64
	HasBodyID   bool
	TotalTravel float64 `sqlref:"body.control.totalTravel"`
}

type StomachContentRow struct {
	SaveID             string
	EntryName          string
	BodyID             int64
	HasBodyID          bool
	ContentIndex       int
	Material           string  `sqlref:"material"`
	Amount             float64 `sqlref:"amount"`
	AverageChunkAmount float64 `sqlref:"averageChunkAmount"`
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
	SpeciesID          int64   `sqlref:"genes.speciesID"`
	Generation         int64   `sqlref:"genes.gen"`
	HatchProgress      float64 `sqlref:"egg.hatchProgress"`
	Energy             float64 `sqlref:"egg.energy"`
	TransformPositionX float64 `sqlref:"transform.position[0]"`
	TransformPositionY float64 `sqlref:"transform.position[1]"`
	TransformRotation  float64 `sqlref:"transform.rotation"`
	TransformScale     float64 `sqlref:"transform.scale"`
	RB2DPX             float64 `sqlref:"rb2d.px"`
	RB2DPY             float64 `sqlref:"rb2d.py"`
	RB2DVX             float64 `sqlref:"rb2d.vx"`
	RB2DVY             float64 `sqlref:"rb2d.vy"`
	RB2DR              float64 `sqlref:"rb2d.r"`
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
	Material             string  `sqlref:"pellet.material"`
	Amount               float64 `sqlref:"pellet.amount"`
	MatterDecayTimeAlive float64 `sqlref:"matterDecay.timeAlive"`
	MatterDecayRotAmount float64 `sqlref:"matterDecay.rotAmount"`
	HasMatterDecay       bool
	TransformPositionX   float64 `sqlref:"transform.position[0]"`
	TransformPositionY   float64 `sqlref:"transform.position[1]"`
	TransformRotation    float64 `sqlref:"transform.rotation"`
	TransformScale       float64 `sqlref:"transform.scale"`
	RB2DPX               float64 `sqlref:"rb2d.px"`
	RB2DPY               float64 `sqlref:"rb2d.py"`
	RB2DVX               float64 `sqlref:"rb2d.vx"`
	RB2DVY               float64 `sqlref:"rb2d.vy"`
	RB2DR                float64 `sqlref:"rb2d.r"`
}

type PheromoneRow struct {
	SaveID             string
	EntryName          string
	PheromoneIndex     int
	TransformPositionX float64 `sqlref:"transform.position[0]"`
	TransformPositionY float64 `sqlref:"transform.position[1]"`
	TransformRotation  float64 `sqlref:"transform.rotation"`
	TransformScale     float64 `sqlref:"transform.scale"`
	HeadingRawJSON     string
	RStrength          float64 `sqlref:"phero.Rstrength"`
	GStrength          float64 `sqlref:"phero.Gstrength"`
	BStrength          float64 `sqlref:"phero.Bstrength"`
	NR                 float64 `sqlref:"phero.nR"`
	NG                 float64 `sqlref:"phero.nG"`
	NB                 float64 `sqlref:"phero.nB"`
}

